//go:build test

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type testTokenEndpoint struct {
	srv           *httptest.Server
	hits          atomic.Int32
	refreshToken  atomic.Value
	resourceValue atomic.Value
}

func newTestTokenEndpoint(t *testing.T) *testTokenEndpoint {
	t.Helper()
	e := &testTokenEndpoint{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		r.ParseForm() //nolint:errcheck
		e.refreshToken.Store(r.FormValue("refresh_token"))
		e.resourceValue.Store(r.FormValue("resource"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "refreshed", "refresh_token": "rotated",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(e.srv.Close)
	return e
}

type testMCPUpstream struct {
	srv           *httptest.Server
	lastAuth      sync.Map
	rejectWith401 atomic.Value
}

func (u *testMCPUpstream) lastAuthFor(method string) string {
	v, _ := u.lastAuth.Load(method)
	s, _ := v.(string)
	return s
}

func (u *testMCPUpstream) serveHTTP(w http.ResponseWriter, r *http.Request) {
	authHdr := r.Header.Get("Authorization")
	var req map[string]any
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	method, _ := req["method"].(string)
	u.lastAuth.Store(method, authHdr)
	if reject, _ := u.rejectWith401.Load().(string); method == "tools/call" && reject != "" && authHdr == reject {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id := req["id"]
	switch method {
	case "initialize":
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
			"result": map[string]any{"protocolVersion": transport.ProtocolVersion}})
	case "tools/list":
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
			"result": map[string]any{"tools": []any{map[string]any{"name": "t1", "inputSchema": map[string]any{}}}}})
	case "tools/call":
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}})
	}
}

func newTestMCPUpstream(t *testing.T) *testMCPUpstream {
	t.Helper()
	u := &testMCPUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(u.serveHTTP))
	t.Cleanup(u.srv.Close)
	return u
}

func oauthServerConfig(name, mcpURL, tokenURL string, enabled bool) config.ServerConfig {
	return config.ServerConfig{
		Name: name, Transport: "http", URL: mcpURL, Enabled: &enabled,
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: tokenURL},
	}
}

type oauthTestSetup struct {
	srv       *server.Server
	token     *testTokenEndpoint
	upstream  *testMCPUpstream
	configDir string
}

func newOAuthTestSetup(t *testing.T, tok *oauth2.Token, opts ...server.ServerOption) *oauthTestSetup {
	t.Helper()
	configDir, token, upstream := t.TempDir(), newTestTokenEndpoint(t), newTestMCPUpstream(t)
	if err := auth.Save(configDir, "live", tok); err != nil {
		t.Fatal(err)
	}
	sc := oauthServerConfig("live", upstream.srv.URL, token.srv.URL, true)
	srv := buildAndStartConnecting(context.Background(),
		BuildServerParams{Cfg: &config.Config{}, ConfigDir: configDir,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Servers: []config.ServerConfig{sc}},
		opts...)
	t.Cleanup(srv.Close)
	awaitConnected(t, srv, "live")
	return &oauthTestSetup{srv: srv, token: token, upstream: upstream, configDir: configDir}
}

func awaitConnected(t *testing.T, srv *server.Server, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.ToolCount(name) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.ToolCount(name) != 1 {
		t.Fatalf("%s upstream: tool count = %d, want 1", name, srv.ToolCount(name))
	}
}

func serveSingleProxyCall(t *testing.T, srv *server.Server, toolName string) map[string]any {
	t.Helper()
	initRaw, _ := json.Marshal(map[string]any{
		"protocolVersion": transport.ProtocolVersion, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "0"},
	})
	initReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": json.RawMessage(initRaw),
	})
	callReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": toolName, "arguments": map[string]any{"args": map[string]any{}}},
	})
	var input bytes.Buffer
	input.Write(append(initReq, '\n'))
	input.Write(append(callReq, '\n'))
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), &input, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return findToolCallResponse(t, out.Bytes())
}

func findToolCallResponse(t *testing.T, data []byte) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(line, &msg) != nil || string(msg.ID) != "1" {
			continue
		}
		var resp map[string]any
		json.Unmarshal(line, &resp) //nolint:errcheck
		return resp
	}
	t.Fatalf("no tools/call response (id=1) in output: %s", data)
	return nil
}

func assertToolCallOK(t *testing.T, resp map[string]any) {
	t.Helper()
	if resp["error"] != nil {
		t.Fatalf("tools/call returned JSON-RPC error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("tools/call isError=true: %v", result)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("tools/call returned empty content")
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	if len(text) == 0 {
		t.Errorf("tools/call returned empty text")
	}
}
