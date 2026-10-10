//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type reloadEnv struct {
	t      *testing.T
	srv    *server.Server
	clock  *clock.Fake
	dir    string
	logs   *syncBuffer
	ticked chan struct{}
}

type reloadEnvParams struct {
	Server      config.ServerConfig
	Projections map[string]*config.ProjectionConfig
}

func newReloadEnv(t *testing.T, p reloadEnvParams) *reloadEnv {
	t.Helper()
	dir := evalTempDir(t)
	if p.Server.Name == "" {
		p.Server.Name = "svc"
	}
	if p.Server.Command == "" {
		p.Server.Command = "echo"
	}
	configtest.WriteServer(t, dir, p.Server)
	if len(p.Projections) != 0 {
		configtest.WriteProjections(t, dir, configtest.ProjectionFile{ServerName: "svc", Tools: p.Projections})
	}
	env := buildReloadEnv(t, dir)
	addReloadUpstream(t, env.srv)
	return env
}

func evalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildReloadEnv(t *testing.T, dir string) *reloadEnv {
	t.Helper()
	return buildReloadEnvWithConfig(t, dir, nil)
}

func buildReloadEnvWithConfig(t *testing.T, dir string, cfg *config.Config) *reloadEnv {
	t.Helper()
	fc := clock.NewFake()
	logs := &syncBuffer{}
	srv := newTestServer(
		t,
		server.Params{Config: cfg, ConfigDir: dir, Logger: slog.New(slog.NewTextHandler(logs, nil)), Clock: fc},
	)
	t.Cleanup(srv.Close)
	return &reloadEnv{t: t, srv: srv, clock: fc, dir: dir, logs: logs, ticked: make(chan struct{}, 64)}
}

func addReloadUpstreamNamed(t *testing.T, srv *server.Server, name string) {
	t.Helper()
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2,\"secret\":\"x\"}"}]}`,
	)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: name}, fake); err != nil {
		t.Fatal(err)
	}
}

func addReloadUpstream(t *testing.T, srv *server.Server) {
	t.Helper()
	addReloadUpstreamNamed(t, srv, "svc")
}

func (e *reloadEnv) startPoller() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.RunConfigReload(ctx, func() { e.ticked <- struct{}{} })
	}()
	e.t.Cleanup(func() { cancel(); <-done })
	e.awaitCheck()
}

func (e *reloadEnv) awaitCheck() {
	e.t.Helper()
	select {
	case <-e.ticked:
	case <-e.t.Context().Done():
		e.t.Fatal("timed out waiting for poll check")
	}
}

func (e *reloadEnv) advanceTick() {
	e.t.Helper()
	e.clock.Advance(server.ConfigPollInterval)
	e.awaitCheck()
}

func (e *reloadEnv) assertServerDataKeys(server string, present, absent []string) {
	e.t.Helper()
	resp := serve(
		e.t,
		e.srv,
		callTool("call", map[string]any{"server": server, "tool": "getData", "params": map[string]any{}}),
	)
	assertProjectedFields(e.t, toolResultText(e.t, resp), present, absent)
}

func (e *reloadEnv) assertDataKeys(present []string, absent []string) {
	e.assertServerDataKeys("svc", present, absent)
}

func (e *reloadEnv) writeRawProjections(content string) {
	e.t.Helper()
	configtest.WriteRawProjections(e.t, e.dir, "svc", content)
}

func (e *reloadEnv) writeProjections(tools map[string]*config.ProjectionConfig) {
	e.t.Helper()
	configtest.WriteProjections(e.t, e.dir, configtest.ProjectionFile{ServerName: "svc", Tools: tools})
}

func reloadCount(e *reloadEnv) int {
	return strings.Count(e.logs.String(), "projections reloaded")
}

func TestProjectionReload_editApplied(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{
		Projections: map[string]*config.ProjectionConfig{"getData": {
			IncludeOnly: []string{"a", "b"},
		}},
	})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	e.writeProjections(map[string]*config.ProjectionConfig{"getData": {IncludeOnly: []string{"a"}}})
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
	if logs := e.logs.String(); !strings.Contains(logs, "projections reloaded") || !strings.Contains(logs, "svc.yaml") {
		t.Errorf("expected INFO naming the changed file, got logs:\n%s", logs)
	}
}

func TestProjectionReload_inlineProjectionRemovalApplied(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{Projections: map[string]*config.ProjectionConfig{
		"getData": {IncludeOnly: []string{"a"}},
	}})
	e.startPoller()
	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
	e.writeProjections(nil)
	e.advanceTick()
	e.assertDataKeys([]string{"a", "b", "secret"}, nil)
}

func TestProjectionReload_createdFileApplied(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b", "secret"}, nil)

	e.writeProjections(map[string]*config.ProjectionConfig{"getData": {IncludeOnly: []string{"a"}}})
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
}

func TestProjectionReload_sameSizeEditDetected(t *testing.T) {
	before, after := "getData:\n  include_only: [a]\n", "getData:\n  include_only: [b]\n"
	if len(before) != len(after) {
		t.Fatal("same-size fixtures differ in size")
	}
	e := newReloadEnv(t, reloadEnvParams{})
	e.writeRawProjections(before)
	e.startPoller()
	e.assertDataKeys([]string{"a"}, []string{"b"})

	e.writeRawProjections(after)
	e.advanceTick()

	e.assertDataKeys([]string{"b"}, []string{"a"})
}

func TestProjectionReload_malformedProjections_keepsPreviousWarnsOnceOthersStillReload(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{
		Projections: map[string]*config.ProjectionConfig{"getData": {
			IncludeOnly: []string{"a"},
		}},
	})
	configtest.WriteServer(t, e.dir, config.ServerConfig{Name: "other", Command: "echo"})
	addReloadUpstreamNamed(t, e.srv, "other")
	e.startPoller()
	e.assertDataKeys([]string{"a"}, []string{"b"})

	e.writeRawProjections("getData:\n  include_only: 5\n")
	configtest.WriteProjections(t, e.dir, configtest.ProjectionFile{
		ServerName: "other",
		Tools: map[string]*config.ProjectionConfig{
			"getData": {
				IncludeOnly: []string{"b"},
			},
		},
	})
	e.advanceTick()
	if logs := e.logs.String(); !strings.Contains(
		logs,
		"projections fail to load, keeping the server's previous projections",
	) {
		t.Errorf("expected WARN for malformed YAML, got logs:\n%s", logs)
	}
	e.assertDataKeys([]string{"a"}, []string{"b"})
	e.assertServerDataKeys("other", []string{"b"}, []string{"a"})

	e.advanceTick()
	if warns := strings.Count(
		e.logs.String(),
		"projections fail to load, keeping the server's previous projections",
	); warns != 1 {
		t.Errorf("expected a single WARN for an unchanged bad file, got %d", warns)
	}

	e.writeProjections(map[string]*config.ProjectionConfig{"getData": {IncludeOnly: []string{"b"}}})
	e.advanceTick()
	e.assertDataKeys([]string{"b"}, []string{"a"})
}

func TestSetProjection_keepsServerFileWhenInlineProjectionsFailToLoad(t *testing.T) {
	cases := map[string]string{
		"broken projection shape":  "command: echo\nprojections:\n  getData:\n    include_only: 5\n",
		"broken projection format": "command: echo\nprojections:\n  getData:\n    format: unknown\n",
		"broken server file":       "command: [unclosed\n",
	}
	for name, serverYAML := range cases {
		t.Run(name, func(t *testing.T) {
			e := newReloadEnv(t, reloadEnvParams{})
			path := config.ServerPath(e.dir, "svc")
			testutil.WriteFile(t, path, serverYAML)
			resp := serve(t, e.srv, callTool("config", map[string]any{
				"action": "set_projection", "server": "svc", "tool": "getData",
				"projection": map[string]any{"include_only": []string{"b"}},
			}))
			if text := toolResultText(t, resp); !strings.Contains(text, "session_only") {
				t.Errorf("set_projection = %s, want it refused with a pointer to session_only", text)
			}
			if after := string(testutil.ReadFile(t, path)); after != serverYAML {
				t.Errorf("server file = %q, want %q kept", after, serverYAML)
			}
		})
	}
}

func TestConfigReload_startupLogsSayWhatHappensToEachBrokenServer(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	e.writeRawProjections("getData:\n  include_only: 5\n")
	testutil.WriteFile(t, filepath.Join(e.dir, "servers", "broken.yaml"), "command: [oops\n")

	e.startPoller()

	logs := e.logs.String()
	for _, want := range []string{
		"server config fails to load, skipping the server",
		"projections fail to load, the server has no projections until the file loads",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("startup logs lack %q; got:\n%s", want, logs)
		}
	}
}

func TestProjectionReload_noChangeNoReload(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{
		Projections: map[string]*config.ProjectionConfig{"getData": {
			IncludeOnly: []string{"a"},
		}},
	})
	e.startPoller()

	e.advanceTick()
	e.advanceTick()
	e.advanceTick()

	if got := reloadCount(e); got != 0 {
		t.Errorf("expected 0 reloads without file changes, got %d", got)
	}
}

func TestProjectionReload_inlineProjectionEditDetected(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b", "secret"}, nil)

	configtest.WriteServer(t, e.dir, config.ServerConfig{
		Name:    "svc",
		Command: "echo",
		Projections: map[string]*config.ProjectionConfig{
			"getData": {
				IncludeOnly: []string{"a"},
			},
		},
	})
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
}

func TestProjectionReload_unreadableServerFileHoldsOnlyItsRules(t *testing.T) {
	dir := evalTempDir(t)
	for _, name := range []string{"held", "kept", "gone"} {
		configtest.WriteServer(
			t,
			dir,
			config.ServerConfig{Name: name, Command: "echo", Projections: map[string]*config.ProjectionConfig{
				"getData": {IncludeOnly: []string{"a"}},
			}},
		)
	}
	env := buildReloadEnv(t, dir)
	for _, name := range []string{"held", "kept", "gone"} {
		addReloadUpstreamNamed(t, env.srv, name)
	}
	env.startPoller()
	for _, name := range []string{"held", "kept", "gone"} {
		env.assertServerDataKeys(name, []string{"a"}, []string{"b", "secret"})
	}

	heldPath := filepath.Join(dir, "servers", "held.yaml")
	if err := os.Remove(heldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(heldPath, 0o700); err != nil {
		t.Fatal(err)
	}
	configtest.WriteServer(
		t,
		dir,
		config.ServerConfig{Name: "kept", Command: "echo", Projections: map[string]*config.ProjectionConfig{
			"getData": {IncludeOnly: []string{"b"}},
		}},
	)
	if err := os.Remove(filepath.Join(dir, "servers", "gone.yaml")); err != nil {
		t.Fatal(err)
	}
	env.advanceTick()

	env.assertServerDataKeys("held", []string{"a"}, []string{"b", "secret"})
	env.assertServerDataKeys("kept", []string{"b"}, []string{"a", "secret"})
	env.assertServerDataKeys("gone", []string{"a", "b", "secret"}, nil)

	if err := os.Remove(heldPath); err != nil {
		t.Fatal(err)
	}
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "held", Command: "echo"})
	env.advanceTick()

	env.assertServerDataKeys("held", []string{"a", "b", "secret"}, nil)
}

func TestProjectionReload_ctxCancelStopsPoller(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.RunConfigReload(ctx, nil)
	}()
	if err := e.clock.BlockUntilContext(t.Context(), responseCleanupTimer+1); err != nil {
		t.Fatal("poll ticker not registered:", err)
	}

	cancel()

	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("poller did not exit after ctx cancel")
	}
}

func TestProjectionReload_setProjectionFinalValuePersistedAndSurvivesReload(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	e.startPoller()

	setDone := make(chan struct{})
	go func() {
		defer close(setDone)
		fields := []string{"b", "a", "b", "a"}
		for _, f := range fields {
			serve(t, e.srv, callTool("config", map[string]any{
				"action": "set_projection", "server": "svc", "tool": "getData",
				"projection": map[string]any{"include_only": []string{f}},
			}))
		}
	}()
	for i := 0; i < 4; i++ {
		e.advanceTick()
	}
	<-setDone
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
	persisted, err := config.LoadServer(e.dir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Projections["getData"].IncludeOnly; !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("persisted include_only = %v, want last set value [a]", got)
	}
}

func TestProjectionReload_actionSurvivesReload(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	e.startPoller()

	e.srv.RegisterAction(config.ActionConfig{
		Name:        "getA",
		Server:      "svc",
		Tool:        "getData",
		Description: "Get field a with defaults",
	})

	e.writeProjections(map[string]*config.ProjectionConfig{"getData": {IncludeOnly: []string{"b"}}})
	e.advanceTick()

	listResp := serve(t, e.srv, callTool("list", map[string]any{"query": "getA"}))
	text := toolResultText(t, listResp)
	var results []map[string]any
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("list response not JSON: %s", text)
	}
	if len(results) == 0 {
		t.Errorf("action getA not found after projection reload: %s", text)
	}
}
