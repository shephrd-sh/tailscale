package main

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestTheListenAddressIsLoopbackOnly(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:0":    true,
		"[::1]:9913":     true,
		"0.0.0.0:9913":   false,
		"192.0.2.1:9913": false,
		"localhost:9913": false,
	} {
		if err := loopback(addr); (err == nil) != ok {
			t.Errorf("loopback(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

// TestAForwardCarriesBothDirectionsToTheirEnds runs forward between two
// loopback connections, as the tailnet's would be, and checks that each end
// reads what the other wrote, then the end of the stream.
func TestAForwardCarriesBothDirectionsToTheirEnds(t *testing.T) {
	far, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer far.Close()
	farSide := make(chan error, 1)
	go func() {
		conn, err := far.Accept()
		if err != nil {
			farSide <- err
			return
		}
		defer conn.Close()
		got, err := io.ReadAll(conn)
		if err == nil && string(got) != "hello" {
			err = errors.New("the far end read " + string(got))
		}
		if err == nil {
			_, err = conn.Write([]byte("ok"))
		}
		farSide <- err
	}()

	client, server := pair(t)
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", far.Addr().String())
	}
	go forward(context.Background(), 1, server, dial, time.Second)
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	client.(*net.TCPConn).CloseWrite()
	answer, err := io.ReadAll(client)
	if err != nil || string(answer) != "ok" {
		t.Fatalf("the client read %q, %v", answer, err)
	}
	if err := <-farSide; err != nil {
		t.Fatal(err)
	}
}

func TestAFailedDialClosesTheConnection(t *testing.T) {
	client, server := pair(t)
	dial := func(ctx context.Context) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go forward(context.Background(), 1, server, dial, 50*time.Millisecond)
	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := client.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("read after a failed dial = %d, %v, want the end of the stream", n, err)
	}
}

// pair returns the two ends of a loopback TCP connection.
func pair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}
