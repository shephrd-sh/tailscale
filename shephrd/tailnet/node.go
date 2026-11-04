package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"tailscale.com/client/local"
	"tailscale.com/control/controlclient"
	"tailscale.com/ipn"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"

	pb "tailscale.com/shephrd/tailnet/tailnetpb"
)

// stateFile is where tsnet keeps the node's identity in its state directory.
const stateFile = "tailscaled.state"

// node is the one tsnet node of the process (tailnet-attach design, decision
// 2): two nodes in one process misroute each other's segments.
//
// One goroutine, run's, owns the node's state and handles commands in order.
// Work that waits, on the control server or the IPN bus, runs on goroutines of
// its own and hands its result back to run through later, tagged with the
// generation it began in; a result from an earlier node is dropped, so it never
// touches a node started after it.
type node struct {
	core *core

	// later carries results back to run; done closes when run returns.
	later chan func()
	done  chan struct{}

	// Owned by run's goroutine.
	server   *tsnet.Server
	client   *local.Client
	stateDir string
	cancel   context.CancelFunc
	// gen rises with each node started and each stopped; live mirrors it for
	// the exit hook, which runs on a goroutine of tsnet's.
	gen  uint64
	live atomic.Uint64
	// last is the latest state event, which a repeated up sends again.
	last *pb.Event

	// ids numbers the connections the node accepts.
	ids atomic.Uint64

	// stable maps each node id the package has seen to its stable id, so a
	// removed peer can be named.
	stableMu sync.Mutex
	stable   map[tailcfg.NodeID]tailcfg.StableNodeID
}

func newNode(c *core) *node {
	n := &node{
		core:   c,
		later:  make(chan func()),
		done:   make(chan struct{}),
		stable: map[tailcfg.NodeID]tailcfg.StableNodeID{},
	}
	// A control-ordered Debug.Exit stops the node, not the process (design,
	// decision 11). The hook runs on the map session's goroutine, which
	// stopping the node waits for, so it only hands the stop to run.
	controlclient.ExitHook = func(code int) {
		// The node generation at the moment of the hook: a down and an up that
		// run before the stop is carried out leave the new node alone.
		gen := n.live.Load()
		go n.handFor(gen, func() {
			if n.stop() {
				n.report(stateEvent(pb.NodeState_NODE_STATE_FAILED, "", fmt.Sprintf("the control server stopped this node (code %d)", code)))
			}
		})
	}
	return n
}

// run carries out commands and hands-back until commands closes, then stops
// the node.
func (n *node) run(commands <-chan *pb.Command) {
	defer close(n.done)
	defer n.stop()
	for {
		select {
		case command, ok := <-commands:
			if !ok {
				return
			}
			n.carryOut(command)
		case f := <-n.later:
			f()
		}
	}
}

// hand runs f on run's goroutine, unless run has returned.
func (n *node) hand(f func()) {
	select {
	case n.later <- f:
	case <-n.done:
	}
}

// handFor runs f on run's goroutine if the node is still generation gen.
func (n *node) handFor(gen uint64, f func()) {
	n.hand(func() {
		if n.gen == gen {
			f()
		}
	})
}

func (n *node) carryOut(command *pb.Command) {
	switch body := command.GetBody().(type) {
	case *pb.Command_Up:
		n.up(body.Up)
	case *pb.Command_Down:
		n.stop()
		n.report(stateEvent(pb.NodeState_NODE_STATE_STOPPED, "", ""))
	case *pb.Command_Login:
		n.login()
	case *pb.Command_Logout:
		n.logout()
	case *pb.Command_Dial:
		n.dial(body.Dial)
	}
}

func (n *node) up(up *pb.Up) {
	// Up on a running node changes nothing and answers ready again, then the
	// latest state, which says whether it runs or waits for a sign-in; a new
	// port or hostname takes a down first.
	if n.server != nil {
		n.core.send(&pb.Event{Body: &pb.Event_Ready{Ready: &pb.Ready{}}})
		if n.last != nil {
			n.core.send(n.last)
		}
		return
	}
	// No log upload and no socket-statistics log: the archive is built with
	// ts_omit_logtail, so neither exists, and the package never sets an
	// environment variable, which cgo would race against the core's threads
	// (design, decision 5).
	server := &tsnet.Server{
		Dir:        up.GetStateDir(),
		Hostname:   up.GetHostname(),
		AuthKey:    up.GetAuthKey(),
		ControlURL: up.GetControlUrl(),
		Logf:       logf("tailnet"),
		UserLogf:   logf("tailnet"),
	}
	// The generation is the new node's before tsnet starts, so an exit the
	// control server orders during start-up or sign-in reaches it.
	n.gen++
	n.live.Store(n.gen)
	failed := func(err error) {
		server.Close()
		n.report(stateEvent(pb.NodeState_NODE_STATE_FAILED, "", err.Error()))
	}
	if err := server.Start(); err != nil {
		failed(err)
		return
	}
	client, err := server.LocalClient()
	if err != nil {
		failed(err)
		return
	}
	listener, err := server.Listen("tcp", ":"+strconv.FormatUint(uint64(up.GetPort()), 10))
	if err != nil {
		failed(err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.server, n.client, n.stateDir, n.cancel, n.last = server, client, up.GetStateDir(), cancel, nil
	go n.watch(ctx, n.gen, server, client)
	go n.accept(ctx, listener, client)
	n.core.send(&pb.Event{Body: &pb.Event_Ready{Ready: &pb.Ready{}}})
}

// stop stops the node, if one runs, waits for tsnet to close, and reports
// whether one ran. Results of work begun on it are dropped from then on.
func (n *node) stop() bool {
	if n.server == nil {
		return false
	}
	n.cancel()
	n.server.Close()
	n.server, n.client, n.cancel, n.last = nil, nil, nil, nil
	n.gen++
	n.live.Store(n.gen)
	return true
}

func (n *node) login() {
	client, gen := n.client, n.gen
	if client == nil {
		return
	}
	// The control server answers when it will; the loop keeps carrying out
	// commands meanwhile.
	go func() {
		if err := client.StartLoginInteractive(context.Background()); err != nil {
			n.handFor(gen, func() {
				n.report(stateEvent(pb.NodeState_NODE_STATE_NEEDS_LOGIN, "", err.Error()))
			})
		}
	}()
}

// logout signs the node out at the control server, then stops it and deletes
// its identity, so the next up signs in as a new device. A logout whose node
// stopped or changed before the control server answered leaves the state on
// disk alone.
func (n *node) logout() {
	client, dir, gen := n.client, n.stateDir, n.gen
	if client == nil {
		return
	}
	go func() {
		logoutErr := client.Logout(context.Background())
		n.hand(func() {
			if n.gen != gen {
				if logoutErr != nil {
					n.core.send(stateEvent(pb.NodeState_NODE_STATE_STOPPED, "", logoutErr.Error()))
				}
				return
			}
			n.stop()
			removeErr := os.Remove(filepath.Join(dir, stateFile))
			if errors.Is(removeErr, os.ErrNotExist) {
				removeErr = nil
			}
			problem := ""
			if err := errors.Join(logoutErr, removeErr); err != nil {
				problem = err.Error()
			}
			n.report(stateEvent(pb.NodeState_NODE_STATE_STOPPED, "", problem))
		})
	}()
}

func (n *node) dial(dial *pb.Dial) {
	server := n.server
	failed := func(err error) {
		n.core.send(&pb.Event{Body: &pb.Event_DialFailed{DialFailed: &pb.DialFailed{Id: dial.GetId(), Error: err.Error()}}})
	}
	if server == nil {
		failed(errors.New("the tailnet node is not running"))
		return
	}
	// A dial ends with its own event and descriptor, and touches no node
	// state, so it runs on its own; on a node stopped meanwhile it fails.
	go func() {
		address := net.JoinHostPort(dial.GetHost(), strconv.FormatUint(uint64(dial.GetPort()), 10))
		conn, err := server.Dial(context.Background(), "tcp", address)
		if err != nil {
			failed(err)
			return
		}
		fd, err := pump(conn)
		if err != nil {
			conn.Close()
			failed(err)
			return
		}
		// The descriptor goes first, so it waits on the socket when the core
		// reads the event.
		if err := n.core.sendFd(dial.GetId(), fd); err != nil {
			failed(err)
			return
		}
		n.core.send(&pb.Event{Body: &pb.Event_Dialed{Dialed: &pb.Dialed{Id: dial.GetId()}}})
	}()
}

// accept hands each caller to the core with its WhoIs identity. A caller
// WhoIs cannot name arrives without a peer, and the core refuses it.
func (n *node) accept(ctx context.Context, listener net.Listener, client *local.Client) {
	defer listener.Close()
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go n.handOver(ctx, conn, client)
	}
}

func (n *node) handOver(ctx context.Context, conn net.Conn, client *local.Client) {
	id := n.ids.Add(1)
	caller := &pb.Caller{Id: id}
	if who, err := client.WhoIs(ctx, conn.RemoteAddr().String()); err == nil && who.Node != nil && who.UserProfile != nil {
		n.remember(who.Node.ID, who.Node.StableID)
		caller.Peer = &pb.Peer{
			StableId:  string(who.Node.StableID),
			NodeName:  who.Node.Name,
			UserId:    uint64(who.UserProfile.ID),
			LoginName: who.UserProfile.LoginName,
			Tags:      who.Node.Tags,
			Shared:    who.Node.Sharer != 0,
		}
	}
	fd, err := pump(conn)
	if err != nil {
		conn.Close()
		return
	}
	if err := n.core.sendFd(id, fd); err != nil {
		return
	}
	n.core.send(&pb.Event{Body: &pb.Event_Caller{Caller: caller}})
}

func (n *node) remember(id tailcfg.NodeID, stable tailcfg.StableNodeID) {
	n.stableMu.Lock()
	defer n.stableMu.Unlock()
	n.stable[id] = stable
}

// watch reads the node's state, sign-in link and peer changes from the IPN
// bus until ctx ends, and hands each to run as generation gen's.
func (n *node) watch(ctx context.Context, gen uint64, server *tsnet.Server, client *local.Client) {
	watcher, err := client.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyPeerChanges)
	if err != nil {
		if ctx.Err() == nil {
			n.handFor(gen, func() {
				n.stop()
				n.report(stateEvent(pb.NodeState_NODE_STATE_FAILED, "", err.Error()))
			})
		}
		return
	}
	defer watcher.Close()
	report := func(event *pb.Event) {
		n.handFor(gen, func() { n.report(event) })
	}
	current := ipn.NoState
	var signIn signIns
	for {
		notify, err := watcher.Next()
		if err != nil {
			return
		}
		if notify.State != nil {
			current = *notify.State
			n.readState(ctx, server, client, current, "", report)
			if signIn.again(current) {
				if err := client.StartLoginInteractive(ctx); err != nil && ctx.Err() == nil {
					report(stateEvent(pb.NodeState_NODE_STATE_NEEDS_LOGIN, "", err.Error()))
				}
			}
		}
		if notify.BrowseToURL != nil && *notify.BrowseToURL != "" {
			report(stateEvent(pb.NodeState_NODE_STATE_NEEDS_LOGIN, *notify.BrowseToURL, ""))
		}
		if notify.ErrMessage != nil {
			n.readState(ctx, server, client, current, *notify.ErrMessage, report)
		}
		if peers := n.peers(notify); len(peers) > 0 {
			event := &pb.Event{Body: &pb.Event_PeersChanged{PeersChanged: &pb.PeersChanged{Peers: peers}}}
			n.handFor(gen, func() { n.core.send(event) })
		}
	}
}

// signIns decides when a node needs a new sign-in link. tsnet asks for one
// at start, so the package asks again only when a node that ran needs a
// sign-in, which means its key expired (design, decision 5). Asking at start
// as well would make tsnet register a second node key, which the control
// server would take for another device.
type signIns struct {
	// ran is whether the node has run since its last sign-in.
	ran bool
}

// again reports whether the node, now in state, needs a new sign-in link.
func (s *signIns) again(state ipn.State) bool {
	switch state {
	case ipn.Running:
		s.ran = true
	case ipn.NeedsLogin:
		again := s.ran
		s.ran = false
		return again
	}
	return false
}

// readState turns an IPN state into a state event for report, reading the
// running node's name and addresses first.
func (n *node) readState(ctx context.Context, server *tsnet.Server, client *local.Client, state ipn.State, problem string, report func(*pb.Event)) {
	switch state {
	case ipn.Running:
		event := stateEvent(pb.NodeState_NODE_STATE_RUNNING, "", problem)
		if status, err := client.StatusWithoutPeers(ctx); err == nil && status.Self != nil {
			event.GetState().Name = status.Self.DNSName
		}
		ip4, ip6 := server.TailscaleIPs()
		for _, ip := range []string{ip4.String(), ip6.String()} {
			if ip != "invalid IP" {
				event.GetState().Addresses = append(event.GetState().Addresses, ip)
			}
		}
		report(event)
	case ipn.NeedsLogin:
		report(stateEvent(pb.NodeState_NODE_STATE_NEEDS_LOGIN, "", problem))
	case ipn.Stopped:
		report(stateEvent(pb.NodeState_NODE_STATE_STOPPED, "", problem))
	default:
		report(stateEvent(pb.NodeState_NODE_STATE_STARTING, "", problem))
	}
}

// peers is each peer the notification changed or removed.
func (n *node) peers(notify ipn.Notify) []*pb.Peer {
	var peers []*pb.Peer
	for _, changed := range notify.PeersChanged {
		n.remember(changed.ID, changed.StableID)
		peers = append(peers, &pb.Peer{
			StableId: string(changed.StableID),
			NodeName: changed.Name,
			UserId:   uint64(changed.User),
			Tags:     changed.Tags,
			Shared:   changed.Sharer != 0,
		})
	}
	n.stableMu.Lock()
	defer n.stableMu.Unlock()
	for _, id := range notify.PeersRemoved {
		stable, ok := n.stable[id]
		if !ok {
			continue
		}
		delete(n.stable, id)
		peers = append(peers, &pb.Peer{StableId: string(stable), Removed: true})
	}
	return peers
}

// report sends a state event and keeps it as the node's latest; it runs on
// run's goroutine.
func (n *node) report(event *pb.Event) {
	n.last = event
	n.core.send(event)
}

func stateEvent(state pb.NodeState, loginURL, problem string) *pb.Event {
	return &pb.Event{Body: &pb.Event_State{State: &pb.State{State: state, LoginUrl: loginURL, Error: problem}}}
}

// logf prints tsnet's log to stderr, which shephrdd writes to core.log, when
// SHEPHRD_TAILNET_LOG is set; otherwise it drops it.
func logf(prefix string) func(string, ...any) {
	if os.Getenv("SHEPHRD_TAILNET_LOG") == "" {
		return func(string, ...any) {}
	}
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "%s: %s\n", prefix, fmt.Sprintf(format, args...))
	}
}
