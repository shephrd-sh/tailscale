package main

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "tailscale.com/shephrd/tailnet/tailnetpb"
)

// pair returns both ends of a Unix socketpair of kind as connections.
func pair(t *testing.T, kind int) (net.Conn, net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, kind, 0)
	if err != nil {
		t.Fatal(err)
	}
	var conns [2]net.Conn
	for i, fd := range fds {
		file := os.NewFile(uintptr(fd), "pair")
		conn, err := net.FileConn(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		conns[i] = conn
	}
	return conns[0], conns[1]
}

// nextEvent reads one framed event from the core's end.
func nextEvent(t *testing.T, conn net.Conn) *pb.Event {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.LittleEndian.Uint32(header[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	event := new(pb.Event)
	if err := proto.Unmarshal(body, event); err != nil {
		t.Fatal(err)
	}
	return event
}

// TestASignalRightAfterTheHelloIsForwarded sends SIGTERM to this process the
// moment the hello arrives, as a supervisor may stop a core right after its
// ready line: the package's handler must already be installed, or the signal
// would end the test process, and the hello stays the first event.
func TestASignalRightAfterTheHelloIsForwarded(t *testing.T) {
	core, control := pair(t, syscall.SOCK_STREAM)
	_, conns := pair(t, syscall.SOCK_STREAM)
	go serve(newCore(control, conns.(*net.UnixConn)))

	if nextEvent(t, core).GetHello() == nil {
		t.Fatal("the first event is not the hello")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	signal := nextEvent(t, core).GetSignal()
	if signal.GetKind() != pb.SignalKind_SIGNAL_KIND_TERM {
		t.Fatalf("the event after the hello is not SIGTERM: %v", signal)
	}
}
