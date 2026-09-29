//go:build integration

package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initHTTPSession(t *testing.T, cfg, token string) string {
	t.Helper()
	resp := daemonPost(t, cfg, daemonPostOpts{Token: token, ToolMode: "compact"})
	sessionID := resp.Header.Get("Mcp-Session-Id")
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if sessionID == "" {
		t.Fatal("expected Mcp-Session-Id from daemon")
	}
	return sessionID
}

func TestIntegrationDaemon_HTTPClientDirect(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	token := readDaemonToken(t, cfg)
	sessionID := initHTTPSession(t, cfg, token)
	resp := postHTTPToolCall(t, cfg, httpToolCall{sessionID: sessionID, token: token, server: "svc", tool: "get_item"})
	assertInlineGetItem(t, decodeDaemonEnvelope(t, resp))
}

func TestIntegrationDaemon_HTTPRejectsMissingToken(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_HTTPRejectsWrongToken(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: "wrong-token-value"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_HostHeaderRejection(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: readDaemonToken(t, cfg), Host: "evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-loopback Host, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_CrossOriginRejection(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: readDaemonToken(t, cfg), Origin: "http://evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin request, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_socketAndDirArePrivate(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)

	si, err := os.Stat(socketPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if si.Mode()&os.ModeSocket == 0 {
		t.Fatalf("expected a Unix socket, got mode %v", si.Mode())
	}
	// Linux honors the socket file's own mode on connect.
	if perm := si.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("socket is group/other-accessible: %04o", perm)
	}
	di, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// macOS ignores the socket file's mode on connect, so the directory's mode is the boundary there.
	if perm := di.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("socket dir is group/other-accessible: %04o", perm)
	}
}

func TestIntegrationDaemon_TokenFilePermissions(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	fi, err := os.Stat(filepath.Join(cfg, "internal", "daemon", "daemon.token"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected token file mode 0600, got %04o", perm)
	}
}

type daemonPostOpts struct {
	Token    string
	Host     string
	Origin   string
	ToolMode string
}

func daemonPost(t *testing.T, cfg string, opts daemonPostOpts) *http.Response {
	t.Helper()
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	if opts.ToolMode != "" {
		params["_mini_tool_mode"] = opts.ToolMode
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params,
	})
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	if opts.Host != "" {
		req.Host = opts.Host
	}
	if opts.Origin != "" {
		req.Header.Set("Origin", opts.Origin)
	}
	resp, err := daemonHTTPClient(cfg).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type httpToolCall struct {
	sessionID string
	token     string
	server    string
	tool      string
}

func postHTTPToolCall(t *testing.T, cfg string, c httpToolCall) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": c.server, "tool": c.tool}},
	})
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", c.sessionID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := daemonHTTPClient(cfg).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeDaemonEnvelope(t *testing.T, resp *http.Response) envelope {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	text := decodeRPCToolText(t, resp.Body)
	var env envelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decode call envelope: %v\ntext: %s", err, text)
	}
	return env
}

func decodeRPCToolText(t *testing.T, body io.Reader) string {
	t.Helper()
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&rpc); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("unexpected rpc error: %#v", rpc.Error)
	}
	if rpc.Result.IsError || len(rpc.Result.Content) == 0 {
		t.Fatal("expected successful tools/call result")
	}
	return rpc.Result.Content[0].Text
}

func assertInlineGetItem(t *testing.T, env envelope) {
	t.Helper()
	if env.Error != "" {
		t.Fatalf("expected ok=true envelope, got %+v", env)
	}
	if env.File != nil {
		t.Fatalf("expected inline response, got file %q", *env.File)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected envelope data object, got %#v", env.Data)
	}
	if data["id"] != float64(1) {
		t.Fatalf("expected data.id=1, got %#v", data["id"])
	}
	if data["name"] != "test" {
		t.Fatalf("expected data.name=test, got %#v", data["name"])
	}
}
