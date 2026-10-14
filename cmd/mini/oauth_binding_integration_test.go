//go:build integration

package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/transport"
)

func TestIntegrationCLI_savedOAuthLoginIsBoundToResource(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{"call", []string{"call", "svc", "t1"}},
		{"perm-call", []string{"perm-call", "svc", "t1"}},
		{"ls", []string{"ls", "svc"}},
		{"status", []string{"status"}},
		{"test", []string{"test"}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			for _, matching := range []bool{true, false} {
				t.Run(map[bool]string{true: "matching", false: "changed URL"}[matching], func(t *testing.T) {
					upstream := newCLIAuthUpstream(t)
					dir := cliAuthConfig(t, cliLoginFixture{Resource: upstream.server.URL, Matching: matching})
					stdout, stderr, code := run(t, miniBin(t), dir, command.args...)
					if matching && code != 0 {
						t.Fatalf("matching login exit=%d: %s%s", code, stdout, stderr)
					}
					if !matching && (code == 0 || !strings.Contains(stdout+stderr, "mini auth svc")) {
						t.Fatalf("mismatched login exit=%d: %s%s", code, stdout, stderr)
					}
					upstream.mu.Lock()
					headers := append([]string(nil), upstream.headers...)
					upstream.mu.Unlock()
					if matching && len(headers) == 0 {
						t.Fatal("positive control reached no upstream")
					}
					for _, header := range headers {
						if !matching && header == "Bearer binding-access" {
							t.Fatal("changed resource received old bearer token")
						}
						if matching && header != "Bearer binding-access" {
							t.Fatalf("matching request authorization=%q", header)
						}
					}
				})
			}
		})
	}
}

type cliAuthUpstream struct {
	server  *httptest.Server
	mu      sync.Mutex
	headers []string
}

func newCLIAuthUpstream(t *testing.T) *cliAuthUpstream {
	t.Helper()
	upstream := &cliAuthUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(upstream.serveHTTP))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *cliAuthUpstream) serveHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.headers = append(u.headers, r.Header.Get("Authorization"))
	u.mu.Unlock()
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusOK)
		return
	}
	var request struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).
		Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": cliAuthResult(request.Method)}); err != nil {
		return
	}
}

func cliAuthResult(method string) any {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": transport.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fixture", "version": "1"},
		}
	case "tools/list":
		return map[string]any{
			"tools": []any{map[string]any{"name": "t1", "inputSchema": map[string]any{"type": "object"}}},
		}
	case "tools/call":
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
	default:
		return map[string]any{}
	}
}

type cliLoginFixture struct {
	Resource string
	Matching bool
}

func cliAuthConfig(t *testing.T, p cliLoginFixture) string {
	t.Helper()
	dir := t.TempDir()
	sc := config.ServerConfig{
		Name:      "svc",
		Transport: config.TransportHTTP,
		URL:       p.Resource,
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "fixture",
			TokenURL: "https://issuer.example.com/token",
		},
	}
	configtest.WriteServer(t, dir, sc)
	boundResource := p.Resource
	if !p.Matching {
		boundResource = "https://old.example.com/mcp"
	}
	login := &auth.Login{
		Resource: boundResource,
		TokenURL: sc.Auth.TokenURL,
		Client:   auth.Client{Source: auth.ClientConfigured, ID: sc.Auth.ClientID},
		Token:    &oauth2.Token{AccessToken: "binding-access"},
	}
	lock := auth.TokenLock{ConfigDir: dir, ServerName: sc.Name, Clock: clock.NewFake()}
	if err := lock.Save(t.Context(), login); err != nil {
		t.Fatal(err)
	}
	return dir
}
