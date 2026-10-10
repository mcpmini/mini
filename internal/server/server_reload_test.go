//go:build test

package server_test

import (
	"bufio"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

type serverReloadEnv struct {
	*reloadEnv
	upstream *httptest.Server
}

func newServerReloadEnv(t *testing.T) *serverReloadEnv {
	t.Helper()
	return &serverReloadEnv{reloadEnv: buildReloadEnv(t, evalTempDir(t)), upstream: newMCPTestServer(t, pingTools)}
}

func (e *serverReloadEnv) serverPath(name string) string {
	return filepath.Join(e.dir, "servers", name+".yaml")
}

func (e *serverReloadEnv) writeServer(server config.ServerConfig) {
	e.t.Helper()
	configtest.WriteServer(e.t, e.dir, server)
}

func (e *serverReloadEnv) removeServerFile(name string) {
	e.t.Helper()
	if err := os.Remove(e.serverPath(name)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *serverReloadEnv) connectConfigured() {
	e.t.Helper()
	servers, err := config.LoadServers(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(servers.Broken) > 0 {
		e.t.Fatalf("broken servers: %+v", servers.Broken)
	}
	e.srv.ConnectUpstreams(e.t.Context(), servers.Loaded)
	e.srv.WaitForStartupConnects()
}

func (e *serverReloadEnv) startWithServers(names ...string) {
	e.t.Helper()
	for _, n := range names {
		e.writeServer(config.ServerConfig{Name: n, Transport: "http", URL: e.upstream.URL})
	}
	e.connectConfigured()
	e.startPoller()
	e.assertConnected(names...)
}

func (e *serverReloadEnv) assertConnected(names ...string) {
	e.t.Helper()
	for _, n := range names {
		if e.srv.ToolCount(n) == 0 {
			e.t.Errorf("%s has no tools, want it connected", n)
		}
	}
}

func (e *serverReloadEnv) assertRemoved(names ...string) {
	e.t.Helper()
	for _, n := range names {
		if got := e.srv.ToolCount(n); got != 0 {
			e.t.Errorf("%s still has %d tools, want it removed", n, got)
		}
	}
}

func TestServerReload_deletedFileOfAnAgentAddedServer_removesIt(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	e := &serverReloadEnv{
		reloadEnv: buildReloadEnvWithConfig(t, evalTempDir(t), cfg),
		upstream:  newMCPTestServer(t, pingTools),
	}
	e.startPoller()
	resp := serve(t, e.srv, callTool("config", map[string]any{
		"action": "add_server", "config": map[string]any{"name": "ag", "transport": "http", "url": e.upstream.URL},
	}))
	if result, _ := resp["result"].(map[string]any); result["isError"] == true {
		t.Fatalf("add_server: %s", toolResultText(t, resp))
	}
	e.advanceTick()

	e.removeServerFile("ag")
	e.advanceTick()

	e.assertRemoved("ag")
}

func TestServerReload_deletedServerFile_removesServerAndNotifiesAgents(t *testing.T) {
	e := newServerReloadEnv(t)
	e.startWithServers("gone", "kept")
	agent := openAgent(t, e.srv)

	e.removeServerFile("gone")
	e.advanceTick()

	e.assertRemoved("gone")
	e.assertConnected("kept")
	agent.waitForLine("notifications/tools/list_changed")
}

func TestServerReload_disabledServer_isRemoved(t *testing.T) {
	t.Run("with working projections", func(t *testing.T) {
		e := newServerReloadEnv(t)
		e.startWithServers("svc")

		disabled := false
		e.writeServer(config.ServerConfig{Name: "svc", Transport: "http", URL: e.upstream.URL, Enabled: &disabled})
		e.advanceTick()

		e.assertRemoved("svc")
	})
	t.Run("even when inline projections fail to load", func(t *testing.T) {
		e := newServerReloadEnv(t)
		e.startWithServers("svc")

		testutil.WriteFile(
			t,
			e.serverPath("svc"),
			"transport: http\nurl: "+e.upstream.URL+"\nenabled: false\nprojections: {tool: {include_only: 5}}\n",
		)
		e.advanceTick()

		e.assertRemoved("svc")
	})
}

func TestServerReload_brokenFileHoldsOnlyItsServer(t *testing.T) {
	breaks := map[string]func(e *serverReloadEnv){
		"malformed": func(e *serverReloadEnv) {
			testutil.WriteFile(e.t, e.serverPath("broken"), "url: [oops\n")
		},
		"unreadable": func(e *serverReloadEnv) {
			e.removeServerFile("broken")
			if err := os.Mkdir(e.serverPath("broken"), 0o700); err != nil {
				e.t.Fatal(err)
			}
		},
	}
	for name, breakFile := range breaks {
		t.Run(name, func(t *testing.T) {
			e := newServerReloadEnv(t)
			e.startWithServers("gone", "broken")

			breakFile(e)
			e.removeServerFile("gone")
			e.advanceTick()
			e.assertRemoved("gone")
			e.assertConnected("broken")

			if err := os.RemoveAll(e.serverPath("broken")); err != nil {
				t.Fatal(err)
			}
			e.advanceTick()
			e.assertRemoved("broken")
		})
	}
}

func TestServerReload_editBeforePollerStarts_isApplied(t *testing.T) {
	e := newServerReloadEnv(t)
	e.writeServer(config.ServerConfig{Name: "svc", Transport: "http", URL: e.upstream.URL})
	e.connectConfigured()

	e.removeServerFile("svc")
	e.startPoller()

	e.assertRemoved("svc")
}

func TestServerReload_removalDuringStartupRetry_staysRemoved(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 1, pingMCPHandler)
	e := newServerReloadEnv(t)
	configtest.WriteServer(t, e.dir, config.ServerConfig{Name: "flaky", Transport: "http", URL: ts.URL})
	e.srv.ConnectUpstreams(t.Context(), []config.ServerConfig{{Name: "flaky", Transport: "http", URL: ts.URL}})
	e.waitForRetryBackoff()

	e.removeServerFile("flaky")
	e.startPoller()
	e.clock.Advance(time.Second)
	e.srv.WaitForStartupConnects()

	e.assertRemoved("flaky")
}

func TestServerReload_leavesAServerWithNoConfigFileAlone(t *testing.T) {
	e := newServerReloadEnv(t)
	e.startWithServers("svc")
	if err := e.srv.AddConnection(t.Context(), config.ServerConfig{Name: "unsaved"}, fakeConn("getData")); err != nil {
		t.Fatal(err)
	}

	e.removeServerFile("svc")
	e.advanceTick()

	e.assertRemoved("svc")
	e.assertConnected("unsaved")
}

func (e *serverReloadEnv) waitForRetryBackoff() {
	e.t.Helper()
	if err := e.clock.BlockUntilContext(e.t.Context(), responseCleanupTimer+1); err != nil {
		e.t.Fatal("startup retry never started its backoff:", err)
	}
}

type agentConn struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan string
}

func openAgent(t *testing.T, srv *server.Server) *agentConn {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Serve(t.Context(), inR, outW) //nolint:errcheck
		outW.Close()
	}()
	a := &agentConn{t: t, in: inW, lines: make(chan string, 64)}
	go a.readLines(outR)
	t.Cleanup(func() { inW.Close(); <-done })
	a.send(rpc("initialize", initParams(true)))
	a.send(notification("notifications/initialized", nil))
	a.send(rpc("tools/list", map[string]any{}))
	a.waitForLine(`"tools"`)
	return a
}

func (a *agentConn) readLines(out io.Reader) {
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		a.lines <- scanner.Text()
	}
	close(a.lines)
}

func (a *agentConn) send(msg []byte) {
	a.t.Helper()
	if _, err := a.in.Write(msg); err != nil {
		a.t.Fatal(err)
	}
}

func (a *agentConn) waitForLine(fragment string) {
	a.t.Helper()
	hangGuard := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-a.lines:
			if !ok {
				a.t.Fatalf("agent stream closed before %s", fragment)
			}
			if strings.Contains(line, fragment) {
				return
			}
		case <-hangGuard:
			a.t.Fatalf("agent never received %s", fragment)
		}
	}
}
