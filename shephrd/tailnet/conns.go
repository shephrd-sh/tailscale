package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
)

// pumping counts the connections pump carries, for the stats event.
var pumping atomic.Int64

// pump returns the core's end of a close-on-exec socketpair whose other end
// carries conn, and closes both when either direction ends. The package's end
// lives only in the goroutines that copy it, so nothing keys a connection by
// its descriptor number (design, decision 2).
func pump(conn net.Conn) (int, error) {
	fds, err := socketpair()
	if err != nil {
		return -1, err
	}
	// Non-blocking, so os.NewFile puts the copies on Go's poller instead of a
	// thread blocked in read(2) per connection. The core's end stays
	// blocking; the binding sets its own mode.
	if err := syscall.SetNonblock(fds[1], true); err != nil {
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		return -1, err
	}
	ours := os.NewFile(uintptr(fds[1]), "tailnet-conn")
	pumping.Add(1)
	var both sync.WaitGroup
	both.Add(2)
	go func() {
		defer both.Done()
		if _, err := io.Copy(ours, conn); err != nil {
			fmt.Fprintf(os.Stderr, "tailnet: connection from %v ended: %v\n", conn.RemoteAddr(), err)
		}
		// The peer finished sending: tell the core, which still reads.
		syscall.Shutdown(fds[1], syscall.SHUT_WR)
	}()
	go func() {
		defer both.Done()
		if _, err := io.Copy(conn, ours); err != nil {
			fmt.Fprintf(os.Stderr, "tailnet: connection to %v ended: %v\n", conn.RemoteAddr(), err)
		}
		// The core never half-closes a connection it still reads, so its end
		// finishing ends the connection: closing conn also stops the copy
		// above, even when the peer is idle.
		conn.Close()
	}()
	go func() {
		both.Wait()
		ours.Close()
		conn.Close()
		pumping.Add(-1)
	}()
	return fds[0], nil
}
