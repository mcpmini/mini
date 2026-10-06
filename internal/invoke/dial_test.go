//go:build test

package invoke

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
		{
			name:         "dangerous_allow_private_urls lets an agent-added server through",
			agentAdded:   true,
			allowPrivate: true,
			wantReached:  true,
		},
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
				Server: config.ServerConfig{
					Name:       "svc",
					Transport:  "http",
					URL:        upstream.URL,
					AgentAdded: tc.agentAdded,
				},
				Clock: clock.System(),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })           // the test fails on hits, not on how the connection closes
			_, _ = conn.Call(t.Context(), "tools/list", nil) // both outcomes are an error; hits tells them apart
			if reached := hits.Load() > 0; reached != tc.wantReached {
				t.Errorf("request reached %s = %v, want %v", upstream.URL, reached, tc.wantReached)
			}
		})
	}
}

func TestDial_agentAddedCommand(t *testing.T) {
	cases := []struct {
		name        string
		agentAdded  bool
		allowStdio  bool
		wantRefusal bool
	}{
		{
			name:        "an agent's command is refused once dangerous_allow_runtime_stdio is off",
			agentAdded:  true,
			wantRefusal: true,
		},
		{name: "dangerous_allow_runtime_stdio still runs an agent's command", agentAdded: true, allowStdio: true},
		{name: "a command the user added runs", allowStdio: false},
	}
	echomcp := os.Getenv("ECHOMCP_BIN")
	if echomcp == "" {
		t.Fatal(
			"ECHOMCP_BIN not set; run check.sh or: go build -o /tmp/echomcp ./cmd/echomcp && ECHOMCP_BIN=/tmp/echomcp go test ...",
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := Dial(t.Context(), DialParams{
				Config: &config.Config{DangerousAllowRuntimeStdio: tc.allowStdio},
				Server: config.ServerConfig{Name: "svc", Command: echomcp, AgentAdded: tc.agentAdded},
				Clock:  clock.System(),
				Logger: slog.New(slog.DiscardHandler),
			})
			if err == nil {
				t.Cleanup(func() { _ = conn.Close() }) // only whether Dial started the command is checked
			}
			if refused := errors.Is(err, ErrAgentCommandNotAllowed); refused != tc.wantRefusal {
				t.Errorf("Dial = %v, refused = %v, want %v", err, refused, tc.wantRefusal)
			}
			if !tc.wantRefusal && err != nil {
				t.Errorf("Dial = %v, want the command started", err)
			}
		})
	}
}

func TestDial_refusesAServerWithAnUnsetVariableWithoutReachingIt(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	t.Cleanup(upstream.Close)
	unset := errors.New("server svc: headers.Authorization: GITHUB_TOKEN isn't set where mini runs")
	server := config.ServerConfig{
		Name:      "svc",
		Transport: "http",
		URL:       upstream.URL,
		Headers:   map[string]string{"Authorization": "Bearer ${GITHUB_TOKEN}"},
		UnsetEnv:  unset,
	}

	_, err := Dial(t.Context(), DialParams{Config: &config.Config{}, Server: server, Clock: clock.System()})

	if !errors.Is(err, unset) {
		t.Errorf("Dial = %v, want the unset variable reported", err)
	}
	if hits.Load() > 0 {
		t.Error("Dial sent the literal ${GITHUB_TOKEN} to the upstream")
	}
}
