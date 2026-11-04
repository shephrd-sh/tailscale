package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"google.golang.org/protobuf/proto"
	"tailscale.com/version"

	pb "tailscale.com/shephrd/tailnet/tailnetpb"
)

// maxFrame bounds a command's body; the largest, an up, is a few hundred
// bytes.
const maxFrame = 64 << 10

// core is the package's side of the two sockets.
type core struct {
	control net.Conn
	conns   *net.UnixConn

	// mu orders whole frames on the control socket.
	mu sync.Mutex
	// fdMu keeps each id whole on the connections stream.
	fdMu sync.Mutex
}

func newCore(control net.Conn, conns *net.UnixConn) *core {
	return &core{control: control, conns: conns}
}

// send writes one event as a frame: a 4-byte little-endian length, then the
// body. A write error means the core is gone, and the read loop ends too.
func (c *core) send(event *pb.Event) {
	body, err := proto.Marshal(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tailnet: encode event: %v\n", err)
		return
	}
	frame := binary.LittleEndian.AppendUint32(make([]byte, 0, 4+len(body)), uint32(len(body)))
	frame = append(frame, body...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.control.Write(frame); err != nil {
		c.control.Close()
	}
}

// sendFd hands the core a connection's descriptor on the connections stream,
// as its 8-byte id with the descriptor attached to the id's first byte, then
// closes the package's copy. A full stream waits for the core to read, so a
// burst of connections is never dropped, as a datagram socket drops them on
// macOS; an error means the core is gone.
func (c *core) sendFd(id uint64, fd int) error {
	defer syscall.Close(fd)
	body := binary.LittleEndian.AppendUint64(nil, id)
	c.fdMu.Lock()
	defer c.fdMu.Unlock()
	n, _, err := c.conns.WriteMsgUnix(body, syscall.UnixRights(fd), nil)
	if err != nil {
		return err
	}
	// The descriptor went with the first byte; the rest of the id follows.
	_, err = c.conns.Write(body[n:])
	return err
}

// read returns the next command, or io.EOF when the core closed the socket.
func (c *core) read() (*pb.Command, error) {
	var header [4]byte
	if _, err := io.ReadFull(c.control, header[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n > maxFrame {
		return nil, fmt.Errorf("a command of %d bytes exceeds %d", n, maxFrame)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.control, body); err != nil {
		return nil, err
	}
	command := new(pb.Command)
	if err := proto.Unmarshal(body, command); err != nil {
		return nil, err
	}
	return command, nil
}

// serve owns SIGTERM and SIGINT, sends the hello, and carries out commands
// until the control socket closes; then it stops the node.
func serve(c *core) {
	// The handlers are installed before the hello: the core starts the
	// package before it says it is ready, so a stop signal sent right after
	// that line is caught. Forwarding starts after the hello, which stays the
	// first event; a signal in between waits in the channel.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	c.send(&pb.Event{Body: &pb.Event_Hello{Hello: &pb.Hello{
		Abi:   abi,
		Build: fmt.Sprintf("tailnet %s tailscale %s", commit, version.Short()),
	}}})
	go forwardSignals(c, signals)
	// The node's own goroutine carries the commands out, so a command that
	// waits never holds up reading the next.
	commands := make(chan *pb.Command)
	go func() {
		defer close(commands)
		for {
			command, err := c.read()
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "tailnet: control socket: %v\n", err)
				}
				return
			}
			commands <- command
		}
	}()
	newNode(c).run(commands)
}

func forwardSignals(c *core, signals <-chan os.Signal) {
	for s := range signals {
		kind := pb.SignalKind_SIGNAL_KIND_TERM
		if s == syscall.SIGINT {
			kind = pb.SignalKind_SIGNAL_KIND_INT
		}
		c.send(&pb.Event{Body: &pb.Event_Signal{Signal: &pb.Signal{Kind: kind}}})
	}
}
