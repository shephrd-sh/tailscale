package main

import (
	"encoding/binary"
	"net"
	"syscall"
	"testing"
	"time"
)

// TestABurstOfHandoversWaitsForTheCore hands over more connections than the
// connections stream holds while the core does not read: every one arrives
// whole and in order once it reads. A datagram socket on macOS drops them
// when its 4 KiB queue fills.
func TestABurstOfHandoversWaitsForTheCore(t *testing.T) {
	coreEnd, ours := pair(t, syscall.SOCK_STREAM)
	c := newCore(nil, ours.(*net.UnixConn))
	const burst = 200
	sent := make(chan error, 1)
	go func() {
		for id := uint64(1); id <= burst; id++ {
			fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
			if err != nil {
				sent <- err
				return
			}
			syscall.Close(fds[1])
			if err := c.sendFd(id, fds[0]); err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
	}()
	// The reader holds back long enough for the stream to fill.
	time.Sleep(500 * time.Millisecond)

	reader := coreEnd.(*net.UnixConn)
	reader.SetReadDeadline(time.Now().Add(10 * time.Second))
	var record [8]byte
	filled, ids, fds := 0, uint64(0), 0
	oob := make([]byte, syscall.CmsgSpace(4*4))
	for ids < burst {
		n, oobn, _, _, err := reader.ReadMsgUnix(record[filled:], oob)
		if err != nil {
			t.Fatalf("after %d ids: %v", ids, err)
		}
		messages, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range messages {
			rights, err := syscall.ParseUnixRights(&message)
			if err != nil {
				t.Fatal(err)
			}
			for _, fd := range rights {
				syscall.Close(fd)
			}
			fds += len(rights)
		}
		filled += n
		if filled < len(record) {
			continue
		}
		filled = 0
		ids++
		if got := binary.LittleEndian.Uint64(record[:]); got != ids {
			t.Fatalf("id %d arrived as id %d", ids, got)
		}
	}
	if fds != burst {
		t.Fatalf("%d descriptors arrived with %d ids", fds, burst)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}
