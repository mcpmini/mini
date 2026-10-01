//go:build test

package invoke

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestDial_privateAddress(t *testing.T) {
	cases := []struct {
		name         string
		agentAdded   bool
		allowPrivate bool
		wantReached  bool
	}{
		{name: "an agent-added server is refused at dial time", agentAdded: true, wantReached: false},
		{name: "dangerous_allow_private_urls lets an agent-added server through", agentAdded: true, allowPrivate: true, wantReached: true},
		{name: "a server the user added is reached", wantReached: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				http.Error(w, "unused", http.StatusInternalServerError)
			}))
			t.Cleanup(upstream.Close)
			conn, err := Dial(t.Context(), DialParams{
				Config: &config.Config{DangerousAllowPrivateURLs: tc.allowPrivate},
				Server: config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL, AgentAdded: tc.agentAdded},
				Clock:  clock.System(),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() }) // the test fails on hits, not on how the connection closes
			_, _ = conn.Call(t.Context(), "tools/list", nil) // both outcomes are an error; hits tells them apart
			if reached := hits.Load() > 0; reached != tc.wantReached {
				t.Errorf("request reached %s = %v, want %v", upstream.URL, reached, tc.wantReached)
			}
		})
	}
}
