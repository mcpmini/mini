//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
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
	srv := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir, Logger: slog.New(slog.NewTextHandler(logs, nil)), Clock: fc})
	t.Cleanup(srv.Close)
	return &reloadEnv{t: t, srv: srv, clock: fc, dir: dir, logs: logs, ticked: make(chan struct{}, 64)}
}

func addReloadUpstreamNamed(t *testing.T, srv *server.Server, name string) {
	t.Helper()
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2,\"secret\":\"x\"}"}]}`)
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
	resp := serve(e.t, e.srv, callTool("call", map[string]any{"server": server, "tool": "getData", "params": map[string]any{}}))
	data := parseProxyEnvelope(e.t, toolResultText(e.t, resp)).Data
	for _, k := range present {
		if data[k] == nil {
			e.t.Errorf("expected field %q present on %q, got: %v", k, server, data)
		}
	}
	for _, k := range absent {
		if data[k] != nil {
			e.t.Errorf("expected field %q absent on %q, got: %v", k, server, data)
		}
	}
}

func (e *reloadEnv) assertDataKeys(present []string, absent []string) {
	e.assertServerDataKeys("svc", present, absent)
}

func (e *reloadEnv) writeRawProjections(content string) {
	e.t.Helper()
	testutil.WriteFile(e.t, filepath.Join(e.dir, "servers", "svc.proj.yaml"), content)
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
	if logs := e.logs.String(); !strings.Contains(logs, "projections reloaded") || !strings.Contains(logs, "svc.proj.yaml") {
		t.Errorf("expected INFO naming the changed file, got logs:\n%s", logs)
	}
}

func TestProjectionReload_deleteRevealsInlineProjections(t *testing.T) {
	inline := config.ServerConfig{
		Command: "echo",
		Projections: map[string]*config.ProjectionConfig{"getData": {
			IncludeOnly: []string{"a"},
		}},
	}
	e := newReloadEnv(t, reloadEnvParams{
		Server: inline,
		Projections: map[string]*config.ProjectionConfig{
			"getData": {
				IncludeOnly: []string{"a", "b"},
			},
		},
	})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	if err := os.Remove(filepath.Join(e.dir, "servers", "svc.proj.yaml")); err != nil {
		t.Fatal(err)
	}
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
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

func TestProjectionReload_malformedProjFile_keepsPreviousWarnsOnceOthersStillReload(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{
		Projections: map[string]*config.ProjectionConfig{"getData": {
			IncludeOnly: []string{"a"},
		}},
	})
	configtest.WriteServer(t, e.dir, config.ServerConfig{Name: "other", Command: "echo"})
	addReloadUpstreamNamed(t, e.srv, "other")
	e.startPoller()
	e.assertDataKeys([]string{"a"}, []string{"b"})

	e.writeRawProjections("getData: [broken\n")
	configtest.WriteProjections(t, e.dir, configtest.ProjectionFile{
		ServerName: "other",
		Tools: map[string]*config.ProjectionConfig{
			"getData": {
				IncludeOnly: []string{"b"},
			},
		},
	})
	e.advanceTick()
	if logs := e.logs.String(); !strings.Contains(logs, "projection reload: skipped server") {
		t.Errorf("expected WARN for malformed YAML, got logs:\n%s", logs)
	}
	e.assertDataKeys([]string{"a"}, []string{"b"})
	e.assertServerDataKeys("other", []string{"b"}, []string{"a"})

	e.advanceTick()
	if warns := strings.Count(e.logs.String(), "projection reload: skipped server"); warns != 1 {
		t.Errorf("expected a single WARN for an unchanged bad file, got %d", warns)
	}

	e.writeProjections(map[string]*config.ProjectionConfig{"getData": {IncludeOnly: []string{"b"}}})
	e.advanceTick()
	e.assertDataKeys([]string{"b"}, []string{"a"})
}

type savedFiles struct {
	serverYAML string
	projYAML   string
}

func TestSetProjection_keepsAProjectionFileThatFailsToLoad(t *testing.T) {
	const savedRules = "getData:\n  include_only: [a]\nother:\n  alias: kept\nbroken: [oops\n"
	cases := map[string]savedFiles{
		"broken projection file":    {serverYAML: "command: echo\n", projYAML: savedRules},
		"broken inline projections": {serverYAML: "command: echo\nprojections:\n  getData:\n    exclude: 3\n", projYAML: "getData:\n  include_only: [a]\n"},
		"broken server file":        {serverYAML: "command: [unclosed\n", projYAML: "getData:\n  include_only: [a]\nother:\n  alias: kept\n"},
	}
	for name, saved := range cases {
		t.Run(name, func(t *testing.T) {
			e := newReloadEnv(t, reloadEnvParams{})
			testutil.WriteFile(t, filepath.Join(e.dir, "servers", "svc.yaml"), saved.serverYAML)
			projPath := filepath.Join(e.dir, "servers", "svc.proj.yaml")
			testutil.WriteFile(t, projPath, saved.projYAML)

			resp := serve(t, e.srv, callTool("config", map[string]any{
				"action": "set_projection", "server": "svc", "tool": "getData",
				"projection": map[string]any{"include_only": []string{"b"}},
			}))

			if text := toolResultText(t, resp); !strings.Contains(text, "session_only") {
				t.Errorf("set_projection = %s, want it refused with a pointer to session_only", text)
			}
			if after := string(testutil.ReadFile(t, projPath)); after != saved.projYAML {
				t.Errorf("svc.proj.yaml = %q, want the user's %q kept", after, saved.projYAML)
			}
		})
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
		configtest.WriteServer(t, dir, config.ServerConfig{Name: name, Command: "echo"})
		configtest.WriteProjections(t, dir, configtest.ProjectionFile{
			ServerName: name,
			Tools: map[string]*config.ProjectionConfig{
				"getData": {
					IncludeOnly: []string{"a"},
				},
			},
		})
	}
	env := buildReloadEnv(t, dir)
	for _, name := range []string{"held", "kept", "gone"} {
		addReloadUpstreamNamed(t, env.srv, name)
	}
	env.startPoller()

	heldPath := filepath.Join(dir, "servers", "held.yaml")
	if err := os.Remove(heldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(heldPath, 0700); err != nil {
		t.Fatal(err)
	}
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "held",
		Tools: map[string]*config.ProjectionConfig{
			"getData": {
				IncludeOnly: []string{"b"},
			},
		},
	})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "kept",
		Tools: map[string]*config.ProjectionConfig{
			"getData": {
				IncludeOnly: []string{"b"},
			},
		},
	})
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

	env.assertServerDataKeys("held", []string{"b"}, []string{"a", "secret"})
}

func TestProjectionReload_ctxCancelStopsPoller(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.RunConfigReload(ctx, nil)
	}()
	if err := e.clock.BlockUntilContext(t.Context(), 2); err != nil {
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
	persisted := testutil.ReadFile(t, filepath.Join(e.dir, "servers", "svc.proj.yaml"))
	if !strings.Contains(string(persisted), "- a") {
		t.Errorf("expected persisted projection to keep last set value, got:\n%s", persisted)
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
