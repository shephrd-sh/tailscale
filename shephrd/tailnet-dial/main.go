// Program tailnet-dial joins a tailnet as its own node and forwards each
// connection it accepts on loopback to one address on that tailnet, so a
// shephrd test reaches a core's tailnet port from another node, with that
// node's identity, over plain TCP, as an app does through the system's
// Tailscale.
//
// It is a test tool. The libshephrdtailnet release ships it as
// bin/tailnet-dial for the tests; nothing shephrd ships to users holds it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tailscale.com/tsnet"
	"tailscale.com/types/logger"
)

var (
	flagControlURL  = flag.String("control-url", "", "the control server's URL")
	flagHostname    = flag.String("hostname", "", "the node's name on the tailnet")
	flagDir         = flag.String("dir", "", "the node's state directory")
	flagTarget      = flag.String("target", "", "host:port on the tailnet each connection is forwarded to")
	flagListen      = flag.String("listen", "127.0.0.1:0", "loopback IP:port to accept connections on, port 0 for any free one")
	flagAuthKey     = flag.String("auth-key", "", "the auth key the node signs in with; none when empty")
	flagDialTimeout = flag.Duration("dial-timeout", 10*time.Second, "how long one dial on the tailnet may take")
	flagVerbose     = flag.Bool("verbose", false, "write the node's own logs to standard error")
)

// main joins the tailnet, then prints "listen <addr>" to standard output, so
// a test that asked for port 0 learns the address once the node runs. Each
// connection it accepts dials --target on its own; one whose dial fails is
// closed, which a client that retries sees as a connection closed before its
// answer.
func main() {
	flag.Parse()
	if *flagControlURL == "" || *flagHostname == "" || *flagDir == "" || *flagTarget == "" {
		log.Fatal("--control-url, --hostname, --dir and --target are required")
	}
	if err := loopback(*flagListen); err != nil {
		log.Fatalf("invalid --listen: %v", err)
	}
	listener, err := net.Listen("tcp", *flagListen)
	if err != nil {
		log.Fatalf("listen on --listen %q: %v", *flagListen, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logf := logger.Discard
	if *flagVerbose {
		logf = log.Printf
	}
	server := &tsnet.Server{
		Dir:        *flagDir,
		Hostname:   *flagHostname,
		ControlURL: *flagControlURL,
		AuthKey:    *flagAuthKey,
		Logf:       logf,
		UserLogf:   log.Printf,
	}
	defer server.Close()
	status, err := server.Up(ctx)
	if err != nil {
		log.Fatalf("join the tailnet: %v", err)
	}
	log.Printf("node %s runs as %v", *flagHostname, status.TailscaleIPs)
	fmt.Printf("listen %s\n", listener.Addr())

	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	dial := func(ctx context.Context) (net.Conn, error) {
		return server.Dial(ctx, "tcp", *flagTarget)
	}
	for id := 1; ; id++ {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("accept: %v", err)
			}
			return
		}
		go forward(ctx, id, conn, dial, *flagDialTimeout)
	}
}

// forward dials once for conn, within timeout, and copies both directions
// until each ends; it closes conn when the dial fails.
func forward(ctx context.Context, id int, conn net.Conn, dial func(context.Context) (net.Conn, error), timeout time.Duration) {
	defer conn.Close()
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	remote, err := dial(dialCtx)
	cancel()
	if err != nil {
		log.Printf("connection %d: dial %s: %v", id, *flagTarget, err)
		return
	}
	defer remote.Close()
	log.Printf("connection %d: reached %s", id, remote.RemoteAddr())
	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		pass(remote, conn)
	}()
	go func() {
		defer copies.Done()
		pass(conn, remote)
	}()
	copies.Wait()
	log.Printf("connection %d: ended", id)
}

// pass copies from to to until from ends, then ends to's writing half, or
// closes it when it has none, so the far end sees the end of the stream.
func pass(to, from net.Conn) {
	_, _ = io.Copy(to, from)
	if half, ok := to.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
		return
	}
	_ = to.Close()
}

// loopback accepts only a numeric loopback IP and a port.
func loopback(addr string) error {
	parsed, err := netip.ParseAddrPort(addr)
	if err != nil {
		return err
	}
	if !parsed.Addr().IsLoopback() {
		return errors.New(addr + " is not a loopback address")
	}
	return nil
}

// init stamps each log line on standard error with UTC microseconds, so a
// test that keeps the log can line it up with the control server's.
func init() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	log.SetPrefix("tailnet-dial: ")
}
