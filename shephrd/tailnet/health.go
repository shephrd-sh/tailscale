package main

import (
	"slices"
	"strings"

	"tailscale.com/health"
)

// healthText joins tsnet's current health warnings into one line, ordered by
// warnable, for a state event's error: it is how tsnet says why the node
// cannot sign in or reach the control server. No warning gives "".
func healthText(state *health.State) string {
	codes := make([]health.WarnableCode, 0, len(state.Warnings))
	for code := range state.Warnings {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	texts := make([]string, 0, len(codes))
	for _, code := range codes {
		if text := state.Warnings[code].Text; text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "; ")
}
