package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "tailscale.com/shephrd/tailnet/tailnetpb"
)

// userPeer is the peer the caller and peers-changed fixtures name.
func userPeer() *pb.Peer {
	return &pb.Peer{
		StableId:  "nFixture1CNTRL",
		NodeName:  "macbook-portable.tail0123.ts.net.",
		UserId:    3_641_851_097_756_277,
		LoginName: "owner@example.com",
	}
}

func command(c *pb.Command) proto.Message { return c }
func event(e *pb.Event) proto.Message     { return e }

// fixtures are the messages in fixtures/v1, one file each. shephrd's binding
// checks its own values against the same files, so both languages agree on
// the wire.
var fixtures = map[string]proto.Message{
	"command_up": command(&pb.Command{Body: &pb.Command_Up{Up: &pb.Up{
		StateDir: "/home/owner/.local/state/shephrd/default/tailnet",
		Hostname: "workstation-shephrd",
		AuthKey:  proto.String("fixture-auth-key"),
		Port:     7443,
	}}}),
	"command_login":      command(&pb.Command{Body: &pb.Command_Login{Login: &pb.Login{}}}),
	"command_read_stats": command(&pb.Command{Body: &pb.Command_ReadStats{ReadStats: &pb.ReadStats{}}}),
	"command_taken":      command(&pb.Command{Body: &pb.Command_Taken{Taken: &pb.Taken{Id: dialBit | 9}}}),
	"command_dial": command(&pb.Command{Body: &pb.Command_Dial{Dial: &pb.Dial{
		Id:   dialBit | 9,
		Host: "workstation-shephrd",
		Port: 7443,
	}}}),
	"event_hello": event(&pb.Event{Body: &pb.Event_Hello{Hello: &pb.Hello{
		Abi:   1,
		Build: "tailnet bcf6dd4cd0b4 tailscale 1.104.1",
	}}}),
	"event_ready": event(&pb.Event{Body: &pb.Event_Ready{Ready: &pb.Ready{}}}),
	"event_stats": event(&pb.Event{Body: &pb.Event_Stats{Stats: &pb.Stats{
		Goroutines:  41,
		Connections: 2,
	}}}),
	"event_state_needs_login": event(&pb.Event{Body: &pb.Event_State{State: &pb.State{
		State:    pb.NodeState_NODE_STATE_NEEDS_LOGIN,
		LoginUrl: "https://login.tailscale.com/a/0123456789abcd",
	}}}),
	"event_caller": event(&pb.Event{Body: &pb.Event_Caller{Caller: &pb.Caller{
		Id:   4,
		Peer: userPeer(),
	}}}),
	"event_dialed": event(&pb.Event{Body: &pb.Event_Dialed{Dialed: &pb.Dialed{
		Id:      dialBit | 9,
		Address: "100.64.0.2",
	}}}),
	"event_dial_failed": event(&pb.Event{Body: &pb.Event_DialFailed{DialFailed: &pb.DialFailed{
		Id:    dialBit | 9,
		Error: "dial tcp 100.64.0.2:7443: connection refused",
	}}}),
	"event_peers_changed": event(&pb.Event{Body: &pb.Event_PeersChanged{PeersChanged: &pb.PeersChanged{
		Peers: []*pb.Peer{
			{
				StableId:  "nFixture2CNTRL",
				NodeName:  "ci-runner.tail0123.ts.net.",
				UserId:    1,
				LoginName: "tagged-devices",
				Tags:      []string{"tag:ci"},
			},
			func() *pb.Peer { p := userPeer(); p.Removed = true; return p }(),
		},
	}}}),
	"event_signal": event(&pb.Event{Body: &pb.Event_Signal{Signal: &pb.Signal{
		Kind: pb.SignalKind_SIGNAL_KIND_TERM,
	}}}),
	"command_up_new_identity": command(&pb.Command{Body: &pb.Command_Up{Up: &pb.Up{
		StateDir:    "/home/owner/.local/state/shephrd/default/tailnet",
		Hostname:    "workstation-shephrd",
		Port:        7443,
		NewIdentity: true,
	}}}),
	"node_record": &pb.NodeRecord{
		StableId:         "nFixture1CNTRL",
		Name:             "workstation-shephrd.example.ts.net.",
		TailscaleVersion: "1.104.1",
	},
}

// TestFixtures encodes each fixture's value and compares the bytes with its
// file, then decodes the file and compares the value. SHEPHRD_WRITE_FIXTURES=1
// writes the files instead. A file without a value fails, so none goes stale.
func TestFixtures(t *testing.T) {
	write := os.Getenv("SHEPHRD_WRITE_FIXTURES") != ""
	paths, err := filepath.Glob("fixtures/v1/*.binpb")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		name := filepath.Base(path)
		if _, ok := fixtures[name[:len(name)-len(".binpb")]]; !ok {
			t.Errorf("%s has no value in fixtures", path)
		}
	}
	for name, value := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("fixtures/v1", name+".binpb")
			encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if write {
				if err := os.WriteFile(path, encoded, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			file, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v; run `mise run tailnet:fixtures`", err)
			}
			if !bytes.Equal(encoded, file) {
				t.Fatalf("the encoding differs from %s", path)
			}
			decoded := value.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(file, decoded); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(decoded.ProtoReflect().GetUnknown()) > 0 {
				t.Fatalf("the fixture holds fields the Go types do not know")
			}
			if !proto.Equal(decoded, value) {
				t.Fatalf("the decoded value differs from %s", path)
			}
		})
	}
}
