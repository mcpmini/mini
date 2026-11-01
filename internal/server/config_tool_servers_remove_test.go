//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

// TestAddRemoveSameServer_ToolsDoNotLeak verifies that after add then remove,
// the server's tools are fully gone from the list.
func TestAddRemoveSameServer_ToolsDoNotLeak(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	ctx := context.Background()

	fake := fakeConn("secret_tool")
	if err := srv.AddConnection(ctx, config.ServerConfig{Name: "tempserver"}, fake); err != nil {
		t.Fatal(err)
	}

	// Verify the tool is visible.
	resp := serve(t, srv, rpc("tools/list", nil))
	if !strings.Contains(toolListJSON(t, resp), "tempserver__secret_tool") {
		t.Fatal("expected secret_tool to be listed after add")
	}

	// Remove the server.
	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "tempserver",
	}))

	// The tool must no longer be discoverable.
	resp = serve(t, srv, rpc("tools/list", nil))
	if strings.Contains(toolListJSON(t, resp), "tempserver") {
		t.Errorf("tempserver tools still visible after remove_server")
	}

	resp = serve(t, srv, callTool("tempserver__secret_tool", map[string]any{}))
	requireRPCError(
		requireRPCErrorParams{T: t, Resp: resp, WantCode: transport.CodeInvalidParams, WantSubstr: "not found"},
	)
}

// TestRemoveServer_ClearsProjections verifies that projections set for a
// server are deleted when the server is removed, so a re-added server with the
// same name doesn't inherit stale projection config.
func TestRemoveServer_ClearsProjections(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	ctx := context.Background()
	writeTestServerConfig(t, srv, config.ServerConfig{Name: "svc"})

	fake := fakeConn("search")
	if err := srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake); err != nil {
		t.Fatal(err)
	}

	// Set a server-scoped projection for svc.search.
	resp := serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "search",
		"projection": map[string]any{"string_limits": map[string]any{"text": 10}},
	}))
	if !strings.Contains(toolResultText(t, resp), `"ok":true`) {
		t.Fatalf("set_projection failed: %v", resp)
	}

	// Verify the projection is visible in status.
	resp = serve(t, srv, callTool("config", map[string]any{"action": "status"}))
	statusText := toolResultText(t, resp)
	if !strings.Contains(statusText, "svc") {
		t.Fatalf("expected svc in status after add: %s", statusText)
	}

	// Remove the server.
	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "svc",
	}))

	// Re-add the server with a different fake that returns large responses.
	// No projections are set this time.
	large := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "search", Description: "search", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"` + strings.Repeat("x", 200) + `"}]}`),
		},
	}
	if err := srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, large); err != nil {
		t.Fatal(err)
	}

	resp = serve(t, srv, callTool("svc__search", map[string]any{}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, strings.Repeat("x", 200)) {
		t.Errorf("stale projection still active after server removal and re-add: %s", text)
	}
}

// An add still in flight when remove_server returns ok:true must not leave the
// server registered; the generation counter in removeGen makes the add abort.
func TestGenerationCounter_RemoveWinsRace(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	ctx := context.Background()

	// dialReady signals that the add goroutine has started dialing (simulated
	// by AddConnection entering before we fire remove). removeStarted blocks
	// add until remove has incremented the generation.
	dialReady := make(chan struct{})
	removeStarted := make(chan struct{})

	addDone := make(chan error, 1)
	go func() {
		slow := &slowFakeConn{
			FakeConnection: fakeConn("toolA"),
			ready:          dialReady,
			block:          removeStarted,
		}
		close(dialReady)
		addDone <- srv.AddConnection(ctx, config.ServerConfig{Name: "racing"}, slow)
	}()

	<-dialReady
	// Remove while add is in-flight (generation counter incremented here).
	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server", "server": "racing",
	}))
	close(removeStarted) // unblock the slow add
	<-addDone            // wait for add to finish

	// After remove returned ok, "racing" must NOT be in the registry.
	resp := serve(t, srv, rpc("tools/list", nil))
	if strings.Contains(toolListJSON(t, resp), "racing") {
		t.Error("remove_server returned ok:true but server is still registered (generation counter not working)")
	}
}

func TestConfigRemoveServer_duringAnAddOfTheSameName_leavesSavedEqualToLive(t *testing.T) {
	e := newConfigToolEnv(t)
	slow, inHandshake, finishHandshake := newGatedMCPServer(t)
	addDone := make(chan struct{})
	go func() {
		defer close(addDone)
		e.addServer(map[string]any{"name": "svc", "transport": "http", "url": slow.URL})
	}()
	<-inHandshake
	removeDone := make(chan struct{})
	go func() {
		defer close(removeDone)
		e.removeServer("svc")
	}()
	e.waitUntilQueuedOrDone("svc", removeDone)

	finishHandshake()
	<-addDone
	<-removeDone

	if saved, live := e.isSaved("svc"), e.srv.ToolCount("svc") > 0; saved || live {
		t.Errorf("saved=%v live=%v, want the remove to run after the add and leave neither", saved, live)
	}
}

func TestConfigAddAndRemoveServer_racingOnOneName_alwaysLeaveSavedEqualToLive(t *testing.T) {
	upstream := newMCPTestServer(t, pingTools)
	for i := range 30 {
		e := newConfigToolEnv(t)
		added := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			defer close(added)
			e.addServer(map[string]any{"name": "svc", "transport": "http", "url": upstream.URL})
		})
		wg.Go(func() {
			for e.srv.ToolCount("svc") == 0 && !isClosed(added) {
				runtime.Gosched()
			}
			e.removeServer("svc")
		})
		wg.Wait()

		if saved, live := e.isSaved("svc"), e.srv.ToolCount("svc") > 0; saved != live {
			t.Fatalf("iteration %d: saved=%v live=%v after both calls returned", i, saved, live)
		}
	}
}

func TestConfigRemoveServer(t *testing.T) {
	t.Run("deletes the server file and disconnects", func(t *testing.T) {
		e := newConfigToolEnv(t)
		if text, failed := e.addServer(
			map[string]any{"name": "svc", "transport": "http", "url": newMCPTestServer(t, pingTools).URL},
		); failed {
			t.Fatalf("add_server: %s", text)
		}

		if text, failed := e.removeServer("svc"); failed {
			t.Fatalf("remove_server: %s", text)
		}

		e.assertNotSaved("svc")
		if e.srv.ToolCount("svc") != 0 {
			t.Error("svc still connected after remove_server")
		}
	})
	t.Run("a server saved again under the name never gets the removed server's login", func(t *testing.T) {
		for _, sameURL := range []bool{true, false} {
			e := newConfigToolEnv(t)
			var oldTokenSent atomic.Int64
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer old-token" {
					oldTokenSent.Add(1)
				}
				fakeMCPHandle(w, r, pingTools)
			})
			removed := httptest.NewServer(handler)
			t.Cleanup(removed.Close)
			authtest.SaveToken(t, authtest.TokenFile{
				ConfigDir:  e.dir,
				ServerName: "svc",
				Server:     userOAuthServer("svc", removed.URL),
				Token:      &oauth2.Token{AccessToken: "old-token"},
			})
			if err := e.connectUserOAuthServer("svc", removed.URL); err != nil || oldTokenSent.Load() == 0 {
				t.Fatalf("precondition: the removed server never used its login: %v", err)
			}
			if text, failed := e.removeServer("svc"); failed {
				t.Fatalf("remove_server: %s", text)
			}
			oldTokenSent.Store(0)
			nextURL := removed.URL
			if !sameURL {
				next := httptest.NewServer(handler)
				t.Cleanup(next.Close)
				nextURL = next.URL
			}

			err := e.connectUserOAuthServer("svc", nextURL)

			if oldTokenSent.Load() != 0 {
				t.Errorf("same URL %v: the new server sent the removed server's token", sameURL)
			}
			if err != nil && strings.Contains(err.Error(), "OAuth configuration changed") {
				t.Errorf("same URL %v: the removed server still holds the name's login: %v", sameURL, err)
			}
		}
	})
	t.Run("disconnects a server with no config file", func(t *testing.T) {
		e := newConfigToolEnv(t)
		if err := e.srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fakeConn("ping")); err != nil {
			t.Fatal(err)
		}

		if text, failed := e.removeServer("svc"); failed {
			t.Fatalf("remove_server: %s", text)
		}

		if e.srv.ToolCount("svc") != 0 {
			t.Error("svc still connected after remove_server")
		}
	})
}

func TestConfigureRemoveServerRequiresName(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
	}))
	assertIsErrorResult(t, resp)
}

func TestConfigureRemoveServer_clearsDiscover(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	addEdgeConn(
		addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "svc"}, Connection: fakeConn("ping")},
	)
	if srv.ToolCount("svc") != 1 {
		t.Fatalf("expected 1 tool before remove")
	}
	assertRemoveOk(t, srv, "svc")

	discoverText := toolListJSON(t, serve(t, srv, rpc("tools/list", nil)))
	if strings.Contains(discoverText, "ping") {
		t.Errorf("removed server's tools should not appear in discover, got: %s", discoverText)
	}
}

type slowFakeConn struct {
	*transport.FakeConnection
	ready chan struct{}
	block chan struct{}
}

func (c *slowFakeConn) ListTools(ctx context.Context) (transport.ToolList, error) {
	<-c.block // wait until remove has fired
	return c.FakeConnection.ListTools(ctx)
}
