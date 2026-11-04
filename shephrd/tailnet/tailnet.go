// Package main is shephrd's tailnet package, built with -buildmode=c-archive
// into libshephrdtailnet.a, which shephrdd links. The C interface is two
// functions; everything else crosses the two sockets shephrd_tailnet_start
// takes as messages of tailnetpb, generated from proto/. README.md has the
// rules.
package main

/*
#include <stdint.h>
*/
import "C"

import (
	"fmt"
	"net"
	"os"
)

// abi is the version of the C interface. It rises with every change to an
// exported function's name, signature or meaning.
const abi = 1

// commit is the fork commit the archive was built from, set with -ldflags
// "-X main.commit=…"; the package reports it in its hello.
var commit string

// shephrd_tailnet_abi returns the C interface's version.
//
//export shephrd_tailnet_abi
func shephrd_tailnet_abi() C.uint32_t {
	return abi
}

// shephrd_tailnet_start takes ownership of two descriptors the core made: one
// end of a stream socketpair that carries framed commands and events, and one
// end of a datagram socketpair that carries each connection's descriptor with
// its id. It returns 0 at once and serves until the control socket closes, or
// -1 when either descriptor is not a socket.
//
//export shephrd_tailnet_start
func shephrd_tailnet_start(control, conns C.int) C.int {
	controlConn, err := adopt(int(control), "tailnet-control")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tailnet: control socket: %v\n", err)
		return -1
	}
	connsConn, err := adopt(int(conns), "tailnet-conns")
	if err != nil {
		controlConn.Close()
		fmt.Fprintf(os.Stderr, "tailnet: connections socket: %v\n", err)
		return -1
	}
	unix, ok := connsConn.(*net.UnixConn)
	if !ok {
		controlConn.Close()
		connsConn.Close()
		return -1
	}
	go serve(newCore(controlConn, unix))
	return 0
}

// adopt wraps a descriptor the core handed over in a net.Conn on Go's poller,
// and closes the original, which net.FileConn duplicated.
func adopt(fd int, name string) (net.Conn, error) {
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	return net.FileConn(file)
}

func main() {}
