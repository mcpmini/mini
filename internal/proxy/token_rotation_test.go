package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func methodOf(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var msg struct {
		Method string `json:"method"`
	}
	json.Unmarshal(body, &msg) //nolint:errcheck
	return msg.Method
}

func TestRun_refreshesTokenAfterUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer newtoken" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	}))
	defer srv.Close()

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n")
	var out strings.Builder
	p := RunParams{
		Port: serverPort(t, srv), SessionID: "sess", Token: "stale",
		ReloadToken: func() (string, error) { return "newtoken", nil },
		In:          in, Out: &out,
	}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), `"result":"ok"`) {
		t.Fatalf("expected recovery after token refresh, got %q", out.String())
	}
}

// Restarted daemon both rotates the token and forgets the session, so recovery
// must walk 401 → refresh → "not initialized" → reinit → success on the new token.
func TestRun_recoversFromFullDaemonRestart(t *testing.T) {
	var initialized atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rotated" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch methodOf(t, r) {
		case "initialize":
			initialized.Store(true)
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":-1,"result":{}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			if !initialized.Load() {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"not initialized"}}`)
				return
			}
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
		}
	}))
	defer srv.Close()

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n")
	var out strings.Builder
	p := RunParams{
		Port: serverPort(t, srv), SessionID: "sess", Token: "stale",
		ReloadToken: func() (string, error) { return "rotated", nil },
		In:          in, Out: &out,
	}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), `"result":"ok"`) {
		t.Fatalf("expected full restart recovery, got %q", out.String())
	}
}

func TestRun_persistentUnauthorizedReturnsErrorEnvelope(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	in := strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/call"}` + "\n")
	var out strings.Builder
	p := RunParams{
		Port: serverPort(t, srv), SessionID: "sess", Token: "stale",
		ReloadToken: func() (string, error) { return "still-stale", nil },
		In:          in, Out: &out,
	}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"error"`) || !strings.Contains(got, `"id":7`) {
		t.Fatalf("expected JSON-RPC error envelope, got %q", got)
	}
	if strings.TrimSpace(got) == "unauthorized" {
		t.Fatal("raw 401 body leaked to agent")
	}
	if hits.Load() != 2 {
		t.Errorf("expected one refresh retry (2 hits), got %d", hits.Load())
	}
}
