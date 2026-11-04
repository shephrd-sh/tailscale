package main

import (
	"testing"

	"tailscale.com/health"
)

// TestHealthTextJoinsTheWarningsInOrder checks that every warning's text
// reaches the state's error, in a stable order, and that none gives "".
func TestHealthTextJoinsTheWarningsInOrder(t *testing.T) {
	state := &health.State{Warnings: map[health.WarnableCode]health.UnhealthyState{
		"network-status": {Text: "Tailscale cannot connect because the network is down."},
		"login-state":    {Text: "You are logged out. The last login error was: connection refused"},
		"empty":          {},
	}}
	want := "You are logged out. The last login error was: connection refused; Tailscale cannot connect because the network is down."
	if got := healthText(state); got != want {
		t.Errorf("healthText = %q, want %q", got, want)
	}
	if got := healthText(&health.State{}); got != "" {
		t.Errorf("no warning gave %q", got)
	}
}
