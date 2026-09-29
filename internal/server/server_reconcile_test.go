//go:build test

package server_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

type reconcileEnv struct {
	*reloadEnv
}

func newReconcileEnv(t *testing.T) *reconcileEnv {
	t.Helper()
	return &reconcileEnv{reloadEnv: buildReloadEnv(t, evalTempDir(t))}
}

func (e *reconcileEnv) serverPath(name string) string {
	return filepath.Join(e.dir, "servers", name+".yaml")
}

func (e *reconcileEnv) writeEchoServer(name, extra string) {
	e.t.Helper()
	writeReloadFile(e.t, e.serverPath(name), fmt.Sprintf("name: %s\ncommand: %s\n%s", name, echomcpBin, extra))
}

func (e *reconcileEnv) removeServerFile(name string) {
	e.t.Helper()
	if err := os.Remove(e.serverPath(name)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *reconcileEnv) connectConfigured() {
	e.t.Helper()
	_, servers, err := config.Load(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	e.srv.ConnectUpstreams(e.t.Context(), servers)
	e.srv.WaitForStartupConnects()
}

func (e *reconcileEnv) startWithEchoServers(names ...string) {
	e.t.Helper()
	for _, n := range names {
		e.writeEchoServer(n, "")
	}
	e.connectConfigured()
	e.startPoller()
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *reconcileEnv) waitForTools(name string) {
	e.t.Helper()
	waitUntil(e.t, name+" tools to appear", func() bool { return e.srv.ToolCount(name) > 0 })
}

func (e *reconcileEnv) waitForNoTools(name string) {
	e.t.Helper()
	waitUntil(e.t, name+" tools to disappear", func() bool { return e.srv.ToolCount(name) == 0 })
}

func (e *reconcileEnv) settle() {
	e.srv.WaitForStartupConnects()
}

func (e *reconcileEnv) registrations(name string) int {
	return strings.Count(e.logs.String(), fmt.Sprintf("msg=\"upstream registered\" server=%s ", name))
}

func (e *reconcileEnv) logged(fragment string) bool {
	return strings.Contains(e.logs.String(), fragment)
}

func (e *reconcileEnv) assertNoReconnect(name string) {
	e.t.Helper()
	e.settle()
	if n := e.registrations(name); n != 1 {
		e.t.Errorf("%s registered %d times, want 1 (no reconnect)", name, n)
	}
	if e.logged("server removed by config change") || e.logged("connecting server from config change") {
		e.t.Errorf("unexpected reconcile action, logs:\n%s", e.logs.String())
	}
}

func TestReconcile_deletedServerFile_removesToolsAndNotifiesAgents(t *testing.T) {
	e := newReconcileEnv(t)
	e.startWithEchoServers("gone", "kept")
	agent := startAgentSession(t, e.srv)

	e.removeServerFile("gone")
	e.advanceTick()

	e.waitForNoTools("gone")
	if e.srv.ToolCount("kept") == 0 {
		t.Error("unrelated server lost its tools")
	}
	waitUntil(t, "tools/list_changed notification", func() bool {
		return strings.Contains(agent.out.String(), "notifications/tools/list_changed")
	})
}

func TestReconcile_newServerFile_connectsWithItsProjections(t *testing.T) {
	e := newReconcileEnv(t)
	e.startWithEchoServers("first")

	e.writeEchoServer("fresh", "projections:\n  echo:\n    alias: shout\n")
	e.advanceTick()

	e.waitForTools("fresh")
	listing := toolResultText(t, serve(t, e.srv, callTool("list", map[string]any{"query": "fresh"})))
	if !strings.Contains(listing, "shout") {
		t.Errorf("new server should come up with its projection alias, list:\n%s", listing)
	}
}

func TestReconcile_changedConnectionSetting_reconnects(t *testing.T) {
	for name, extra := range map[string]string{
		"args": "args: [--changed]\n",
		"env":  "env: [SETTING=changed]\n",
	} {
		t.Run(name, func(t *testing.T) {
			e := newReconcileEnv(t)
			e.startWithEchoServers("svc")

			e.writeEchoServer("svc", extra)
			e.advanceTick()

			waitUntil(t, "second registration", func() bool { return e.registrations("svc") == 2 })
			e.waitForTools("svc")
		})
	}
}

func TestReconcile_editBetweenStartupLoadAndPollerStart_isApplied(t *testing.T) {
	for _, tc := range []struct {
		name      string
		editInGap func(e *reconcileEnv)
		server    string
		wantTools bool
	}{
		{name: "removed file", editInGap: func(e *reconcileEnv) { e.removeServerFile("gap") }, server: "gap", wantTools: false},
		{name: "added file", editInGap: func(e *reconcileEnv) { e.writeEchoServer("late", "") }, server: "late", wantTools: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newReconcileEnv(t)
			e.writeEchoServer("gap", "")
			baseline := server.CaptureConfigBaseline(e.dir)
			_, servers, err := config.Load(e.dir)
			if err != nil {
				t.Fatal(err)
			}
			tc.editInGap(e)
			e.srv.ConnectUpstreams(t.Context(), servers)
			e.settle()
			e.startPollerFrom(baseline)

			e.advanceTick()

			if tc.wantTools {
				e.waitForTools(tc.server)
			} else {
				e.waitForNoTools(tc.server)
			}
		})
	}
}

func TestReconcile_inlineConfigServers_followConfigYAML(t *testing.T) {
	e := newReconcileEnv(t)
	configYAML := filepath.Join(e.dir, "config.yaml")
	writeReloadFile(t, configYAML, fmt.Sprintf("servers:\n- name: old\n  command: %s\n", echomcpBin))
	e.connectConfigured()
	e.startPoller()

	writeReloadFile(t, configYAML, fmt.Sprintf("servers:\n- name: new\n  command: %s\n", echomcpBin))
	e.advanceTick()

	e.waitForNoTools("old")
	e.waitForTools("new")
}

func TestReconcile_changedSettingsWithInvalidProjection_keepsPreviousProjections(t *testing.T) {
	e := newReconcileEnv(t)
	e.startWithEchoServers("svc")
	e.writeEchoServer("svc", "projections:\n  echo:\n    alias: shout\n")
	e.advanceTick()
	if listing := e.listTools("svc"); !strings.Contains(listing, "shout") {
		t.Fatalf("alias not applied before the edit, list:\n%s", listing)
	}

	e.writeEchoServer("svc", "args: [--changed]\nprojections:\n  echo:\n    alias: shout\n    format: bogus\n")
	e.advanceTick()

	waitUntil(t, "second registration", func() bool { return e.registrations("svc") == 2 })
	e.waitForTools("svc")
	if listing := e.listTools("svc"); !strings.Contains(listing, "shout") {
		t.Errorf("reconnected svc lost its previous projections, list:\n%s", listing)
	}
}

func (e *reconcileEnv) listTools(query string) string {
	e.t.Helper()
	return toolResultText(e.t, serve(e.t, e.srv, callTool("list", map[string]any{"query": query})))
}

func TestReconcile_unchangedSettings_neverReconnect(t *testing.T) {
	t.Run("projections only", func(t *testing.T) {
		e := newReconcileEnv(t)
		e.startWithEchoServers("svc")

		e.writeEchoServer("svc", "projections:\n  echo:\n    include_only: [message]\n")
		e.advanceTick()

		e.assertNoReconnect("svc")
	})
	t.Run("reformatted file", func(t *testing.T) {
		e := newReconcileEnv(t)
		e.startWithEchoServers("svc")

		writeReloadFile(t, e.serverPath("svc"), fmt.Sprintf("# reformatted\ncommand: %q\n\n\nname:   svc\n", echomcpBin))
		e.advanceTick()

		e.assertNoReconnect("svc")
	})
	t.Run("oauth marker written by the daemon", func(t *testing.T) {
		e := newReconcileEnv(t)
		e.startWithEchoServers("svc")

		if err := config.MarkOAuthDetected(e.dir, "svc"); err != nil {
			t.Fatal(err)
		}
		e.writeEchoServer("svc", "# touched\n")
		e.advanceTick()

		e.assertNoReconnect("svc")
	})
}

func TestReconcile_invalidInput_neverDisconnectsWorkingServers(t *testing.T) {
	t.Run("source error blocks removals until the file loads again", func(t *testing.T) {
		e := newReconcileEnv(t)
		e.startWithEchoServers("broken", "victim")

		writeReloadFile(t, e.serverPath("broken"), "name: broken\ncommand: [oops\n")
		e.removeServerFile("victim")
		e.advanceTick()
		e.settle()

		if e.srv.ToolCount("broken") == 0 || e.srv.ToolCount("victim") == 0 {
			t.Fatal("servers were disconnected while a config file failed to load")
		}
		if !e.logged("source error") {
			t.Errorf("expected the source error to be logged, logs:\n%s", e.logs.String())
		}

		e.writeEchoServer("broken", "# fixed\n")
		e.advanceTick()
		e.waitForNoTools("victim")
		e.assertToolsKept("broken")
	})
	t.Run("undefined env var keeps the running server", func(t *testing.T) {
		e := newReconcileEnv(t)
		e.startWithEchoServers("svc")

		e.writeEchoServer("svc", "env: [TOKEN=${MINI_TEST_UNDEFINED_197}]\n")
		e.advanceTick()

		e.assertNoReconnect("svc")
		if !e.logged("undefined environment variable") {
			t.Errorf("expected a warning naming the undefined variable, logs:\n%s", e.logs.String())
		}
	})
}

func (e *reconcileEnv) assertToolsKept(name string) {
	e.t.Helper()
	if e.srv.ToolCount(name) == 0 {
		e.t.Errorf("%s lost its tools", name)
	}
}

func TestReconcile_enabledFalse_removesAndReEnableReconnects(t *testing.T) {
	e := newReconcileEnv(t)
	e.startWithEchoServers("svc")

	e.writeEchoServer("svc", "enabled: false\n")
	e.advanceTick()
	e.waitForNoTools("svc")

	e.writeEchoServer("svc", "enabled: true\n")
	e.advanceTick()
	e.waitForTools("svc")
	if n := e.registrations("svc"); n != 2 {
		t.Errorf("svc registered %d times, want 2", n)
	}
}

func TestReconcile_runtimeAddedServers_areLeftAlone(t *testing.T) {
	t.Run("not removed when its file disappears", func(t *testing.T) {
		e := newReconcileEnv(t)
		e.startWithEchoServers("svc")
		if err := e.srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc", RuntimeAdded: true}, fakeConn("runtimeTool")); err != nil {
			t.Fatal(err)
		}

		e.removeServerFile("svc")
		e.advanceTick()
		e.settle()

		e.assertToolsKept("svc")
		if !e.logged("leaving runtime-added server") {
			t.Errorf("expected the skipped removal to be logged, logs:\n%s", e.logs.String())
		}
	})
	t.Run("a file with the same name does not replace it", func(t *testing.T) {
		e := newReconcileEnv(t)
		e.startWithEchoServers("other")
		if err := e.srv.AddConnection(t.Context(), config.ServerConfig{Name: "rt", RuntimeAdded: true}, fakeConn("runtimeTool")); err != nil {
			t.Fatal(err)
		}

		e.writeEchoServer("rt", "")
		e.advanceTick()
		e.settle()

		listing := toolResultText(t, serve(t, e.srv, callTool("list", map[string]any{"query": "runtimeTool"})))
		if !strings.Contains(listing, "runtimeTool") {
			t.Errorf("runtime server was replaced, list:\n%s", listing)
		}
		if !e.logged("a runtime-added server has this name") {
			t.Errorf("expected a conflict warning, logs:\n%s", e.logs.String())
		}
	})
}

func TestReconcile_removalDuringStartupRetry_staysRemoved(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 1, pingMCPHandler)
	e := newReconcileEnv(t)
	writeReloadFile(t, e.serverPath("flaky"), "name: flaky\ntransport: http\nurl: "+ts.URL+"\n")
	e.startPoller()
	e.srv.ConnectUpstreams(t.Context(), []config.ServerConfig{{Name: "flaky", Transport: "http", URL: ts.URL}})
	e.waitForBackoffTimer(3)

	e.removeServerFile("flaky")
	e.advanceTick()
	e.settle()

	if n := e.srv.ToolCount("flaky"); n != 0 {
		t.Errorf("removed server came back with %d tools after its startup retry", n)
	}
}

func TestReconcile_connectingNewServer_doesNotCancelOtherStartupRetries(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 2, pingMCPHandler)
	e := newReconcileEnv(t)
	e.startPoller()
	e.srv.ConnectUpstreams(t.Context(), []config.ServerConfig{{Name: "slow", Transport: "http", URL: ts.URL}})
	e.waitForBackoffTimer(3)

	e.writeEchoServer("fresh", "")
	e.advanceTick()
	e.waitForTools("fresh")
	waitUntil(t, "second startup failure", func() bool {
		return strings.Count(e.logs.String(), "upstream unavailable at startup, retrying") == 2
	})
	e.waitForBackoffTimer(3)
	e.clock.Advance(2 * time.Second)

	e.waitForTools("slow")
}

func (e *reconcileEnv) waitForBackoffTimer(count int) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), 10*time.Second)
	defer cancel()
	if err := e.clock.BlockUntilContext(ctx, count); err != nil {
		e.t.Fatalf("waiting for %d clock waiters: %v", count, err)
	}
}

type agentSession struct {
	out *syncBuffer
}

func startAgentSession(t *testing.T, srv *server.Server) agentSession {
	t.Helper()
	pr, pw := io.Pipe()
	out := &syncBuffer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Serve(t.Context(), pr, out) //nolint:errcheck
	}()
	t.Cleanup(func() { pw.Close(); <-done })
	pw.Write(buildServeInput(true, [][]byte{notification("notifications/initialized", nil), rpc("tools/list", map[string]any{})})) //nolint:errcheck
	waitUntil(t, "agent handshake", func() bool { return strings.Count(out.String(), `"id":1`) >= 2 })
	return agentSession{out: out}
}
