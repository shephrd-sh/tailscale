// Program testcontrol runs Tailscale's test control server, with its DERP and
// STUN servers, for shephrd's tailnet tests, and lets a test in another
// process drive it through an admin API on loopback: complete a sign-in, tag
// a node, expire every key, or order a node to exit.
//
// It is a test tool. The libshephrdtailnet release ships it as
// bin/testcontrol for the tests; nothing shephrd ships to users holds it.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"testing"

	"tailscale.com/tailcfg"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"
)

var (
	flagAddr        = flag.String("addr", "127.0.0.1:9911", "IP:port the control server listens on; the DERP and STUN servers also run on that IP")
	flagAdminAddr   = flag.String("admin-addr", "", "loopback IP:port of the admin API; none when empty")
	flagRequireAuth = flag.Bool("require-auth", false, "make a node without a valid auth key wait for a sign-in, which POST /login completes")
	flagAuthKey     = flag.String("auth-key", "", "the auth key every node must present; any key when empty")
)

func main() {
	flag.Parse()
	host, _, err := net.SplitHostPort(*flagAddr)
	if err != nil {
		log.Fatalf("invalid --addr %q: %v", *flagAddr, err)
	}
	control := &testcontrol.Server{
		DERPMap:         integration.RunDERPAndSTUN(tb{}, logger.Discard, host),
		ExplicitBaseURL: "http://" + *flagAddr,
		RequireAuth:     *flagRequireAuth,
		RequireAuthKey:  *flagAuthKey,
	}
	if *flagAdminAddr != "" {
		if err := loopback(*flagAdminAddr); err != nil {
			log.Fatalf("invalid --admin-addr: %v", err)
		}
		go func() {
			log.Fatal(http.ListenAndServe(*flagAdminAddr, admin(control)))
		}()
	}
	log.Printf("listening on %s", *flagAddr)
	log.Fatal(http.ListenAndServe(*flagAddr, control))
}

// loopback refuses an address that is not a loopback IP and port, so the
// admin API is never reachable from another host.
func loopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%q is not an IP address: %w", host, err)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("%s is not a loopback address", ip)
	}
	return nil
}

// node is one node as GET /nodes lists it.
type node struct {
	Name   string   `json:"name"`
	UserID int64    `json:"user_id"`
	Tags   []string `json:"tags"`
}

// admin serves the admin API:
//
//	GET  /nodes                       the nodes, as JSON
//	POST /login?url=<sign-in link>    completes the sign-in the link names
//	POST /tags?name=<host>&tag=<tag>  sets the node's tags, the tag parameter repeated
//	POST /expire?expired=true|false   expires every node key, or ends that
//	POST /debug-exit?name=<host>&code=<n>  sends the node a map response with Debug.Exit
func admin(control *testcontrol.Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /nodes", func(w http.ResponseWriter, r *http.Request) {
		nodes := []node{}
		for _, n := range control.AllNodes() {
			nodes = append(nodes, node{
				Name:   n.Hostinfo.Hostname(),
				UserID: int64(n.User),
				Tags:   n.Tags,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(nodes)
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if !control.CompleteAuth(r.URL.Query().Get("url")) {
			http.Error(w, "no sign-in waits on that link", http.StatusNotFound)
		}
	})
	mux.HandleFunc("POST /tags", func(w http.ResponseWriter, r *http.Request) {
		n, err := byName(control, r.URL.Query().Get("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		n.Tags = r.URL.Query()["tag"]
		control.UpdateNode(n)
	})
	mux.HandleFunc("POST /expire", func(w http.ResponseWriter, r *http.Request) {
		expired, err := strconv.ParseBool(r.URL.Query().Get("expired"))
		if err != nil {
			http.Error(w, "expired must be true or false", http.StatusBadRequest)
			return
		}
		control.SetExpireAllNodes(expired)
	})
	mux.HandleFunc("POST /debug-exit", func(w http.ResponseWriter, r *http.Request) {
		code, err := strconv.Atoi(r.URL.Query().Get("code"))
		if err != nil {
			http.Error(w, "code must be an integer", http.StatusBadRequest)
			return
		}
		n, err := byName(control, r.URL.Query().Get("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if !control.AddRawMapResponse(n.Key, &tailcfg.MapResponse{Debug: &tailcfg.Debug{Exit: &code}}) {
			http.Error(w, "the node is not polling", http.StatusConflict)
		}
	})
	return mux
}

// byName finds the node whose host name is name.
func byName(control *testcontrol.Server, name string) (*tailcfg.Node, error) {
	for _, n := range control.AllNodes() {
		if n.Hostinfo.Hostname() == name {
			return n, nil
		}
	}
	return nil, errors.New("no node has that name")
}

// tb is the testing.TB RunDERPAndSTUN takes, for a server that runs outside a
// test: a failure ends the process.
type tb struct {
	testing.TB
}

func (tb) Helper()                           {}
func (tb) Cleanup(func())                    {}
func (tb) Logf(format string, args ...any)   { log.Printf(format, args...) }
func (tb) Fatalf(format string, args ...any) { log.Fatalf(format, args...) }
func (tb) Fatal(args ...any)                 { log.Fatal(args...) }
func (tb) Errorf(format string, args ...any) { log.Fatalf(format, args...) }
func (tb) Error(args ...any)                 { log.Fatal(args...) }
