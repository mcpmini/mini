//go:build test

package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestNotification_toolsChangedAfterAddServer(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	cfg.DangerousAllowPrivateURLs = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	msgs := serveAll(t, srv,
		callTool("config", map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "dynamic", "transport": "http", "url": mcp.URL},
		}),
	)

	if !hasNotification(msgs, "notifications/tools/list_changed") {
		t.Errorf("expected notifications/tools/list_changed after add_server; got: %v", msgs)
	}
}

func TestNotification_toolsChangedAfterRemoveServer(t *testing.T) {
	srv := newTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConn("aTool")) //nolint:errcheck

	msgs := serveAll(t, srv,
		callTool("config", map[string]any{"action": "remove_server", "server": "svc"}),
	)

	if !hasNotification(msgs, "notifications/tools/list_changed") {
		t.Errorf("expected notifications/tools/list_changed after remove_server; got: %v", msgs)
	}
}

func TestNotification_noNotificationForOtherConfigureActions(t *testing.T) {
	msgs := serveAll(t, newTestServer(t),
		callTool("config", map[string]any{"action": "status"}),
	)

	for _, m := range msgs {
		if m["method"] == "notifications/tools/list_changed" {
			t.Errorf("unexpected tools/list_changed for configure status: %v", m)
		}
	}
}

// TestNotification_compactSessionAddServer_notifiesProxySession proves that when a
// compact session calls add_server, a proxy session on a different connection receives
// notifications/tools/list_changed. This is the cross-mode notification bug that
// notifyAllSessions was introduced to fix.
func TestNotification_compactSessionAddServer_notifiesProxySession(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	cfg.DangerousAllowPrivateURLs = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	// Start a long-lived proxy session via pipes so it stays alive across the
	// compact session's add_server call. Use an output pipe so we can scan lines
	// without races — the scanner owns the read side exclusively.
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	proxyDone := make(chan error, 1)
	go func() { proxyDone <- srv.Serve(ctx, inR, outW) }()

	// Collect all proxy output lines into a channel so the scanner goroutine
	// is the sole reader of outR and the assertion loop reads from the channel.
	lines := make(chan []byte, 64)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			b := make([]byte, len(scanner.Bytes()))
			copy(b, scanner.Bytes())
			lines <- b
		}
		close(lines)
	}()

	// Send the proxy initialize and wait for its response so the session is
	// in the store before the compact session calls add_server.
	inW.Write(buildServeInput(false, nil)) //nolint:errcheck // proxy mode (compact=false)

	waitForInitialize := func() bool {
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-deadline:
				return false
			case line, ok := <-lines:
				if !ok {
					return false
				}
				var m map[string]any
				if json.Unmarshal(line, &m) != nil {
					continue
				}
				if res, _ := m["result"].(map[string]any); res != nil {
					if _, ok := res["protocolVersion"]; ok {
						return true
					}
				}
			}
		}
	}

	if !waitForInitialize() {
		inW.Close()
		t.Fatal("proxy session did not initialize within timeout")
	}

	// Compact session calls add_server — this must notify all sessions including
	// the proxy session above.
	serveAll(t, srv,
		callTool("config", map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "dynamic", "transport": "http", "url": mcp.URL},
		}),
	)

	// Close the proxy input pipe so its Serve loop exits and the output pipe writer
	// is closed, which terminates the scanner and closes the lines channel.
	inW.Close()
	<-proxyDone
	outW.Close()

	// Drain remaining lines and check for the notification.
	found := false
	for line := range lines {
		var m map[string]any
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m["method"] == transport.NotificationToolsChanged && m["id"] == nil {
			found = true
		}
	}
	if !found {
		t.Error("proxy session did not receive notifications/tools/list_changed after compact session called add_server")
	}
}
