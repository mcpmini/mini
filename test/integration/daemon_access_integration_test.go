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

	"github.com/mcpmini/mini/internal/transport"
)

func initHTTPSession(t *testing.T, cfg, token string) string {
	t.Helper()
	resp := daemonPost(t, cfg, daemonPostOpts{Token: token})
	sessionID := resp.Header.Get("Mcp-Session-Id")
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if sessionID == "" {
		t.Fatal("expected Mcp-Session-Id from daemon")
	}
	return sessionID
}

func TestIntegrationDaemon_HTTPClientDirect(t *testing.T) {
	t.Parallel()
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	token := readDaemonToken(t, cfg)
	sessionID := initHTTPSession(t, cfg, token)
	resp := postHTTPToolCall(t, cfg, httpToolCall{sessionID: sessionID, token: token, server: "svc", tool: "get_item"})
	assertInlineGetItem(t, decodeDaemonEnvelope(t, resp))
}

func TestIntegrationDaemon_HTTPRejectsMissingToken(t *testing.T) {
	t.Parallel()
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_HTTPRejectsWrongToken(t *testing.T) {
	t.Parallel()
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: "wrong-token-value"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_HostHeaderRejection(t *testing.T) {
	t.Parallel()
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: readDaemonToken(t, cfg), Host: "evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-loopback Host, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_CrossOriginRejection(t *testing.T) {
	t.Parallel()
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: readDaemonToken(t, cfg), Origin: "http://evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin request, got %d", resp.StatusCode)
	}
}

func TestIntegrationDaemon_socketAndDirArePrivate(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	fi, err := os.Stat(filepath.Join(cfg, "internal", "daemon", "daemon.token"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected token file mode 0600, got %04o", perm)
	}
}

type daemonPostOpts struct {
	Token  string
	Host   string
	Origin string
}

func daemonPost(t *testing.T, cfg string, opts daemonPostOpts) *http.Response {
	t.Helper()
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "initialize", "params": params,
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
	session := daemonSession{cfg: cfg, id: c.sessionID, token: c.token}
	return postDaemonToolsCall(t, session, toolCallRaw(toolCallParams{
		Server: c.server,
		Tool:   c.tool,
		Args:   map[string]any{},
	}))
}

type daemonSession struct {
	cfg   string
	id    string
	token string
}

func postDaemonToolsCall(t *testing.T, s daemonSession, params map[string]any) *http.Response {
	t.Helper()
	body, _ := json.Marshal(
		map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": 2, "method": "tools/call", "params": params},
	)
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", s.id)
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := daemonHTTPClient(s.cfg).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func waitForDaemonServersConnected(t *testing.T, cfg string) {
	t.Helper()
	names := connectableServers(t, cfg, nil)
	if len(names) == 0 {
		return
	}
	token := readDaemonToken(t, cfg)
	session := daemonSession{cfg: cfg, id: initHTTPSession(t, cfg, token), token: token}
	waitUntilConnected(t, names, func() map[string]bool {
		resp := postDaemonToolsCall(t, session, configStatusParams())
		defer resp.Body.Close()
		return connectedInStatus(t, decodeRPCToolText(t, resp.Body))
	})
}

func decodeDaemonEnvelope(t *testing.T, resp *http.Response) nativeCallResult {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatal(err)
	}
	return decodeNativeCall(t, rpc.Result)
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

func assertInlineGetItem(t *testing.T, env nativeCallResult) {
	t.Helper()
	if env.IsError {
		t.Fatalf("expected ok=true envelope, got %+v", env)
	}
	if env.Notice.File != "" {
		t.Fatalf("expected inline response, got file %q", env.Notice.File)
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
