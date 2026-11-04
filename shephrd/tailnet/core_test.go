package main

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
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
	if len(c.held) != burst {
		t.Fatalf("the package holds %d descriptors, not %d", len(c.held), burst)
	}
	for id := uint64(1); id <= burst; id++ {
		c.taken(id)
	}
	if len(c.held) != 0 {
		t.Fatalf("the package still holds %d descriptors once all are taken", len(c.held))
	}
}

// TestAHandedDescriptorStaysOpenUntilTaken closes the core's copy at once: the
// connection stays open through the package's copy until the core takes it.
func TestAHandedDescriptorStaysOpenUntilTaken(t *testing.T) {
	coreEnd, ours := pair(t, syscall.SOCK_STREAM)
	c := newCore(nil, ours.(*net.UnixConn))
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	peerFile := os.NewFile(uintptr(fds[1]), "peer")
	peer, err := net.FileConn(peerFile)
	peerFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := c.sendFd(5, fds[0]); err != nil {
		t.Fatal(err)
	}
	reader := coreEnd.(*net.UnixConn)
	reader.SetReadDeadline(time.Now().Add(10 * time.Second))
	var record [8]byte
	oob := make([]byte, syscall.CmsgSpace(4))
	if _, oobn, _, _, err := reader.ReadMsgUnix(record[:], oob); err != nil {
		t.Fatal(err)
	} else {
		messages, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err != nil || len(messages) != 1 {
			t.Fatalf("%d control messages: %v", len(messages), err)
		}
		rights, err := syscall.ParseUnixRights(&messages[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range rights {
			syscall.Close(fd)
		}
	}

	var one [1]byte
	peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := peer.Read(one[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the connection ended before the core took it: %v", err)
	}
	c.taken(5)
	peer.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := peer.Read(one[:]); err != io.EOF {
		t.Fatalf("the connection stayed open once taken: %v", err)
	}
}
