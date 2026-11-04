package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "tailscale.com/shephrd/tailnet/tailnetpb"
)

// recorded returns a state directory whose node ran under Tailscale ran,
// with its state file holding state.
func recorded(t *testing.T, ran string, state []byte) string {
	t.Helper()
	dir := t.TempDir()
	record := &pb.NodeRecord{StableId: "nNODE", Name: "core.example.ts.net.", TailscaleVersion: ran}
	if err := writeRecord(dir, record); err != nil {
		t.Fatal(err)
	}
	if state != nil {
		if err := os.WriteFile(filepath.Join(dir, stateFile), state, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestAStateThatOutlivedItsRecordIsRefused checks the guard decision 5 asks
// for: a node record whose state went missing, emptied or came from a newer
// Tailscale stops the node, where tsnet would sign in as a new device or
// misread the state.
func TestAStateThatOutlivedItsRecordIsRefused(t *testing.T) {
	for _, test := range []struct {
		name  string
		dir   string
		wants string
	}{
		{"no record", t.TempDir(), ""},
		{"a signed-in state", recorded(t, "1.104.1", []byte(`{"_machinekey":"x"}`)), ""},
		{"an older Tailscale's state", recorded(t, "1.98.0", []byte(`{}`)), ""},
		{"a deleted state", recorded(t, "1.104.1", nil), "the tailnet state is gone"},
		{"an emptied state", recorded(t, "1.104.1", []byte{}), "the tailnet state is gone"},
		{"a newer Tailscale's state", recorded(t, "1.106.0", []byte(`{}`)), "newer than this build's 1.104.1"},
	} {
		err := checkState(test.dir, "1.104.1")
		switch {
		case test.wants == "" && err != nil:
			t.Errorf("%s: %v", test.name, err)
		case test.wants != "" && (err == nil || !strings.Contains(err.Error(), test.wants)):
			t.Errorf("%s: got %v, want an error with %q", test.name, err, test.wants)
		}
	}
	corrupt := t.TempDir()
	if err := os.WriteFile(filepath.Join(corrupt, recordFile), []byte{0xff, 0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkState(corrupt, "1.104.1"); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("a corrupt record: got %v", err)
	}
}

// TestANewIdentitySetsOnlyThePackagesFilesAside checks that a new identity
// moves tsnet's state and the record aside, keeps the core's files, and
// leaves a directory a fresh node starts from.
func TestANewIdentitySetsOnlyThePackagesFilesAside(t *testing.T) {
	dir := recorded(t, "1.104.1", []byte(`{}`))
	if err := os.WriteFile(filepath.Join(dir, "up.binpb"), []byte("core"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setIdentityAside(dir, time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	aside := filepath.Join(dir, "replaced-20261104T120000.000000000Z")
	for _, name := range []string{stateFile, recordFile} {
		if _, err := os.Stat(filepath.Join(aside, name)); err != nil {
			t.Errorf("%s is not set aside: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s is still in place: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "up.binpb")); err != nil {
		t.Errorf("the core's file moved: %v", err)
	}
	if err := checkState(dir, "1.104.1"); err != nil {
		t.Errorf("a fresh node is refused: %v", err)
	}
	info, err := os.Stat(filepath.Join(aside, recordFile))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the record is mode %o, want 600", mode)
	}
}
