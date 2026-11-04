package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"tailscale.com/atomicfile"
	"tailscale.com/util/cmpver"

	pb "tailscale.com/shephrd/tailnet/tailnetpb"
)

// recordFile is the node record the package keeps beside tsnet's state once
// the node has run (tailnet-attach design, decision 5): it says the state
// directory held a signed-in node, so a state that went missing is reported,
// never replaced by a new identity. tsnet would treat an empty state file as
// missing and sign in as a new device.
const recordFile = "node.pb"

// checkState returns why a node must not start from dir, run by Tailscale
// version tailscale, or nil when it may.
func checkState(dir, tailscale string) error {
	bytes, err := os.ReadFile(filepath.Join(dir, recordFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("the tailnet node record cannot be read: %w", err)
	}
	record := new(pb.NodeRecord)
	if err := proto.Unmarshal(bytes, record); err != nil {
		return fmt.Errorf("the tailnet node record is corrupt: %w", err)
	}
	state, err := os.Stat(filepath.Join(dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && state.Size() == 0) {
		return fmt.Errorf("the tailnet state is gone: %s was signed in here; a new identity signs in as a new device", record.GetName())
	}
	if err != nil {
		return fmt.Errorf("the tailnet state cannot be read: %w", err)
	}
	// tsnet has no downgrade path for its state.
	if cmpver.Compare(record.GetTailscaleVersion(), tailscale) > 0 {
		return fmt.Errorf("the tailnet state was written by Tailscale %s, newer than this build's %s", record.GetTailscaleVersion(), tailscale)
	}
	return nil
}

// ownsFile reports whether name, in a state directory, is the package's:
// tsnet's state and logs, its certificates, and the node record. The core's
// own files there stay where they are.
func ownsFile(name string) bool {
	return name == recordFile || name == "certs" || strings.HasPrefix(name, "tailscaled")
}

// setIdentityAside moves the package's files in dir into a replaced-<time>
// directory beside them, so the next start signs in as a new device and the
// old identity stays on disk.
func setIdentityAside(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("the tailnet state cannot be listed: %w", err)
	}
	aside := filepath.Join(dir, "replaced-"+now.UTC().Format("20060102T150405.000000000Z"))
	made := false
	for _, entry := range entries {
		if !ownsFile(entry.Name()) {
			continue
		}
		if !made {
			if err := os.Mkdir(aside, 0o700); err != nil {
				return fmt.Errorf("the old tailnet identity cannot be set aside: %w", err)
			}
			made = true
		}
		if err := os.Rename(filepath.Join(dir, entry.Name()), filepath.Join(aside, entry.Name())); err != nil {
			return fmt.Errorf("the old tailnet identity cannot be set aside: %w", err)
		}
	}
	return nil
}

// writeRecord stores record in dir, mode 0600, through a file renamed into
// place, so a crash leaves the old record or the new one.
func writeRecord(dir string, record *pb.NodeRecord) error {
	bytes, err := proto.Marshal(record)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(dir, recordFile), bytes, 0o600)
}
