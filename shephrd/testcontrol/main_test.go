package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tailscale.com/tstest/integration/testcontrol"
)

func TestTheAdminAPIBindsOnlyToLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:9912": true,
		"[::1]:9912":     true,
		"0.0.0.0:9912":   false,
		"192.0.2.1:9912": false,
		"localhost:9912": false,
		"9912":           false,
	} {
		if err := loopback(addr); (err == nil) != ok {
			t.Errorf("loopback(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

func TestTheAdminAPIRefusesWhatItCannotDo(t *testing.T) {
	api := admin(&testcontrol.Server{})
	for _, c := range []struct {
		method, target string
		status         int
	}{
		{"GET", "/nodes", http.StatusOK},
		{"POST", "/login?url=http://127.0.0.1/auth/missing", http.StatusNotFound},
		{"POST", "/tags?name=absent&tag=tag:ci", http.StatusNotFound},
		{"POST", "/expire?name=absent&expired=maybe", http.StatusBadRequest},
		{"POST", "/expire?name=absent&expired=true", http.StatusNotFound},
		{"POST", "/debug-exit?name=absent&code=1", http.StatusNotFound},
		{"POST", "/debug-exit?name=absent&code=x", http.StatusBadRequest},
		{"GET", "/login", http.StatusMethodNotAllowed},
	} {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest(c.method, c.target, nil))
		if w.Code != c.status {
			t.Errorf("%s %s = %d, want %d: %s", c.method, c.target, w.Code, c.status, w.Body)
		}
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest("GET", "/nodes", nil))
	var nodes []node
	if err := json.Unmarshal(w.Body.Bytes(), &nodes); err != nil || len(nodes) != 0 {
		t.Errorf("GET /nodes on an empty tailnet = %q, %v", w.Body, err)
	}
}
