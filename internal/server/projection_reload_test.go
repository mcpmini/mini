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
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const reloadPollInterval = 5 * time.Second

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
	ServerYAML string
	ProjYAML   string
}

func newReloadEnv(t *testing.T, p reloadEnvParams) *reloadEnv {
	t.Helper()
	dir := evalTempDir(t)
	if p.ServerYAML == "" {
		p.ServerYAML = "name: svc\ncommand: echo\n"
	}
	writeReloadFile(t, filepath.Join(dir, "servers", "svc.yaml"), p.ServerYAML)
	if p.ProjYAML != "" {
		writeReloadFile(t, filepath.Join(dir, "servers", "svc.proj.yaml"), p.ProjYAML)
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
	fc := clock.NewFake()
	logs := &syncBuffer{}
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(logs, nil)), server.WithClock(fc))
	t.Cleanup(srv.Close)
	return &reloadEnv{t: t, srv: srv, clock: fc, dir: dir, logs: logs, ticked: make(chan struct{}, 64)}
}

func addReloadUpstream(t *testing.T, srv *server.Server) {
	t.Helper()
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2,\"secret\":\"x\"}"}]}`)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fake); err != nil {
		t.Fatal(err)
	}
}

func (e *reloadEnv) startPoller() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.RunProjectionReload(ctx, func() { e.ticked <- struct{}{} })
	}()
	e.t.Cleanup(func() { cancel(); <-done })
	if err := e.clock.BlockUntilContext(e.t.Context(), 2); err != nil {
		e.t.Fatal("poll ticker not registered:", err)
	}
}

func (e *reloadEnv) advanceTick() {
	e.t.Helper()
	e.clock.Advance(reloadPollInterval)
	select {
	case <-e.ticked:
	case <-e.t.Context().Done():
		e.t.Fatal("timed out waiting for poll tick")
	}
}

func (e *reloadEnv) callData() map[string]any {
	e.t.Helper()
	resp := serve(e.t, e.srv, callTool("call", map[string]any{"server": "svc", "tool": "getData", "params": map[string]any{}}))
	return parseProxyEnvelope(e.t, toolResultText(e.t, resp)).Data
}

func (e *reloadEnv) assertDataKeys(present []string, absent []string) {
	e.t.Helper()
	data := e.callData()
	for _, k := range present {
		if data[k] == nil {
			e.t.Errorf("expected field %q present, got: %v", k, data)
		}
	}
	for _, k := range absent {
		if data[k] != nil {
			e.t.Errorf("expected field %q absent, got: %v", k, data)
		}
	}
}

func (e *reloadEnv) writeProjFile(content string) {
	e.t.Helper()
	writeReloadFile(e.t, filepath.Join(e.dir, "servers", "svc.proj.yaml"), content)
}

func writeReloadFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func reloadCount(e *reloadEnv) int {
	return strings.Count(e.logs.String(), "projections reloaded")
}

func TestProjectionReload_editApplied(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a, b]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a", "b"}, []string{"secret"})

	e.writeProjFile("getData:\n  include_only: [a]\n")
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
	if logs := e.logs.String(); !strings.Contains(logs, "projections reloaded") || !strings.Contains(logs, "svc.proj.yaml") {
		t.Errorf("expected INFO naming the changed file, got logs:\n%s", logs)
	}
}

func TestProjectionReload_deleteRevealsInlineProjections(t *testing.T) {
	inline := "name: svc\ncommand: echo\nprojections:\n  getData:\n    include_only: [a]\n"
	e := newReloadEnv(t, reloadEnvParams{ServerYAML: inline, ProjYAML: "getData:\n  include_only: [a, b]\n"})
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

	e.writeProjFile("getData:\n  include_only: [a]\n")
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
}

func TestProjectionReload_sameSizeEditDetected(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a"}, []string{"b"})

	e.writeProjFile("getData:\n  include_only: [b]\n")
	e.advanceTick()

	e.assertDataKeys([]string{"b"}, []string{"a"})
}

func TestProjectionReload_malformedYAMLKeepsPreviousUntilValidWrite(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a]\n"})
	e.startPoller()
	e.assertDataKeys([]string{"a"}, []string{"b"})

	e.writeProjFile("getData: [broken\n")
	e.advanceTick()
	if logs := e.logs.String(); !strings.Contains(logs, "projection reload failed") {
		t.Errorf("expected WARN for malformed YAML, got logs:\n%s", logs)
	}
	e.assertDataKeys([]string{"a"}, []string{"b"})

	e.advanceTick()
	if warns := strings.Count(e.logs.String(), "projection reload failed"); warns != 1 {
		t.Errorf("expected a single WARN for an unchanged bad file, got %d", warns)
	}

	e.writeProjFile("getData:\n  include_only: [b]\n")
	e.advanceTick()
	e.assertDataKeys([]string{"b"}, []string{"a"})
}

func TestProjectionReload_noChangeNoReload(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{ProjYAML: "getData:\n  include_only: [a]\n"})
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

	writeReloadFile(t, filepath.Join(e.dir, "servers", "svc.yaml"),
		"name: svc\ncommand: echo\nprojections:\n  getData:\n    include_only: [a]\n")
	e.advanceTick()

	e.assertDataKeys([]string{"a"}, []string{"b", "secret"})
}

func TestProjectionReload_configYAMLInlineProjectionEditApplied(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [a]\n")
	env := buildReloadEnv(t, dir)
	addReloadUpstream(t, env.srv)
	env.startPoller()
	env.assertDataKeys([]string{"a"}, []string{"b", "secret"})

	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [b]\n")
	env.advanceTick()

	env.assertDataKeys([]string{"b"}, []string{"a", "secret"})
}

func TestProjectionReload_configYAMLCreatedAppliesInlineProjections(t *testing.T) {
	dir := evalTempDir(t)
	env := buildReloadEnv(t, dir)
	addReloadUpstream(t, env.srv)
	env.startPoller()
	env.assertDataKeys([]string{"a", "b", "secret"}, nil)

	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [a]\n")
	env.advanceTick()

	env.assertDataKeys([]string{"a"}, []string{"b", "secret"})
}

func TestProjectionReload_configYAMLDeletedRemovesInlineProjections(t *testing.T) {
	dir := evalTempDir(t)
	writeReloadFile(t, filepath.Join(dir, "config.yaml"),
		"servers:\n- name: svc\n  command: echo\n  projections:\n    getData:\n      include_only: [a]\n")
	env := buildReloadEnv(t, dir)
	addReloadUpstream(t, env.srv)
	env.startPoller()
	env.assertDataKeys([]string{"a"}, []string{"b", "secret"})

	if err := os.Remove(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	env.advanceTick()

	env.assertDataKeys([]string{"a", "b", "secret"}, nil)
}

func TestProjectionReload_ctxCancelStopsPoller(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.RunProjectionReload(ctx, nil)
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

func TestProjectionReload_tickRacingSetProjectionKeepsFinalState(t *testing.T) {
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
	persisted, err := os.ReadFile(filepath.Join(e.dir, "servers", "svc.proj.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), "- a") {
		t.Errorf("expected persisted projection to keep last set value, got:\n%s", persisted)
	}
}

func TestProjectionReload_runtimeServerProjectionSurvivesReload(t *testing.T) {
	e := newReloadEnv(t, reloadEnvParams{})
	e.startPoller()

	// Add a runtime server with inline projections (RuntimeAdded=true, no disk YAML).
	runtimeFake := fakeConn("getData")
	runtimeFake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2,\"secret\":\"x\"}"}]}`)
	proj := map[string]*config.ProjectionConfig{"getData": {IncludeOnly: []string{"a"}}}
	e.srv.AddConnection(t.Context(), config.ServerConfig{Name: "rt", RuntimeAdded: true, Projections: proj}, runtimeFake)

	// Trigger a reload (simulated by editing the disk server's proj file).
	e.writeProjFile("getData:\n  include_only: [b]\n")
	e.advanceTick()

	// "svc" projection changed on disk (now shows b, not a).
	e.assertDataKeys([]string{"b"}, []string{"a", "secret"})

	// Runtime server's projection must survive the reload.
	rtResp := serve(t, e.srv, callTool("call", map[string]any{
		"server": "rt", "tool": "getData", "params": map[string]any{},
	}))
	data := parseProxyEnvelope(t, toolResultText(t, rtResp)).Data
	if data["a"] == nil {
		t.Errorf("runtime server's projection was wiped by reload: got %v", data)
	}
	if data["secret"] != nil {
		t.Errorf("runtime server's projection was applied: secret should be absent, got %v", data)
	}
}

func TestInstallUpstreamLocked_addUpstreamPreservesLiveProjection(t *testing.T) {
	srv := newConfigServer(t)
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fake)

	// Set a live projection (as reconnectWithToken would see after a set_projection edit).
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "getData",
		"projection": map[string]any{"include_only": []string{"a"}},
	}))

	// Simulate reconnectWithToken: re-add with stale snapshot projections;
	// the live projection from set_projection must not be reverted.
	newFake := fakeConn("getData")
	newFake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	srv.AddConnection(t.Context(), config.ServerConfig{
		Name: "svc",
		Projections: map[string]*config.ProjectionConfig{
			"getData": {IncludeOnly: []string{"b"}},
		},
	}, newFake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "getData", "params": map[string]any{},
	}))
	data := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["a"] == nil {
		t.Errorf("live projection was reverted by re-AddUpstream: got %v", data)
	}
	if data["b"] != nil {
		t.Errorf("stale snapshot projection was applied: b should be absent, got %v", data)
	}
}

func TestInstallUpstreamLocked_removeAndReAddGetsNewProjections(t *testing.T) {
	srv := newConfigServer(t)
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	srv.AddConnection(t.Context(), config.ServerConfig{
		Name: "svc",
		Projections: map[string]*config.ProjectionConfig{
			"getData": {IncludeOnly: []string{"a"}},
		},
	}, fake)

	// Remove the server (clears live projections).
	assertRemoveOk(t, srv, "svc")

	// Re-add with different projections — the new config must take effect.
	newFake := fakeConn("getData")
	newFake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	srv.AddConnection(t.Context(), config.ServerConfig{
		Name: "svc",
		Projections: map[string]*config.ProjectionConfig{
			"getData": {IncludeOnly: []string{"b"}},
		},
	}, newFake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "getData", "params": map[string]any{},
	}))
	data := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["b"] == nil {
		t.Errorf("expected new projection after remove+add, got %v", data)
	}
	if data["a"] != nil {
		t.Errorf("old projection still active after remove+add, got %v", data)
	}
}
