package main

import (
	"testing"

	"tailscale.com/ipn"
)

// TestASignInIsAskedForOnlyAfterTheNodeRan walks the states a node passes
// through: the sign-in tsnet asks for at start is not asked for again, and a
// node that ran and then needs a sign-in, as when its key expires, gets a new
// link once.
func TestASignInIsAskedForOnlyAfterTheNodeRan(t *testing.T) {
	steps := []struct {
		state ipn.State
		again bool
	}{
		{ipn.NoState, false},
		{ipn.NeedsLogin, false},
		{ipn.Starting, false},
		{ipn.Running, false},
		{ipn.NeedsLogin, true},
		{ipn.NeedsLogin, false},
		{ipn.Starting, false},
		{ipn.Running, false},
		{ipn.Stopped, false},
		{ipn.NeedsLogin, true},
	}
	var signIn signIns
	for i, step := range steps {
		if got := signIn.again(step.state); got != step.again {
			t.Errorf("step %d, %v: again = %v, want %v", i, step.state, got, step.again)
		}
	}
}
