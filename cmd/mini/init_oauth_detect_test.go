//go:build test

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func upstreamAnswering(t *testing.T, status int, challenge string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if challenge != "" {
			w.Header().Set("WWW-Authenticate", challenge)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type requestLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *requestLog) record(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !slices.Contains(l.paths, path) {
		l.paths = append(l.paths, path)
	}
}

func (l *requestLog) seen() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Sorted(slices.Values(l.paths))
}

func recordingUpstream(t *testing.T) (string, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, log
}

func hangingUpstream(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv.URL
}

func importFromMCPJSON(t *testing.T, configDir, mcpServers string) []string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "claude.json")
	writeImportSource(t, src, `{"mcpServers": `+mcpServers+`}`)
	return captureImport(t, configDir, src)
}

func captureImport(t *testing.T, configDir, src string) []string {
	t.Helper()
	var names []string
	testutil.CaptureStdout(t, func() { names = importClaudeFormat(configDir, "Claude Code", src) })
	return names
}

func detect(configDir string, names []string) {
	detectImportedOAuth(oauthDetectParams{configDir: configDir, names: names, clock: clock.System(), errOut: &bytes.Buffer{}})
}

func loginListing(t *testing.T, configDir string) string {
	t.Helper()
	out := &bytes.Buffer{}
	runLoginStep(loginStepParams{configDir: configDir, ask: func(string) string { return "s" }, out: out, errOut: &bytes.Buffer{}})
	return out.String()
}

func TestDetectImportedOAuthMakesLoginStepListChallengedServer(t *testing.T) {
	configDir := t.TempDir()
	url := upstreamAnswering(t, http.StatusUnauthorized, "Bearer")
	names := importFromMCPJSON(t, configDir, `{"svc": {"type": "http", "url": "`+url+`"}}`)
	if strings.Contains(loginListing(t, configDir), "svc") {
		t.Fatal("login step listed svc before it was probed")
	}

	detect(configDir, names)

	if !config.IsOAuthDetected(configDir, "svc") {
		t.Error("OAuth marker not written for a 401 Bearer upstream")
	}
	if got := loginListing(t, configDir); !strings.Contains(got, "svc (no token)") {
		t.Errorf("login step output = %q, want svc listed", got)
	}
}

func TestDetectImportedOAuthMarksEveryChallengedServer(t *testing.T) {
	configDir := t.TempDir()
	url := upstreamAnswering(t, http.StatusUnauthorized, "Bearer")
	names := importFromMCPJSON(t, configDir, `{"a": {"type": "http", "url": "`+url+`/a"}, "b": {"type": "http", "url": "`+url+`/b"}}`)

	detect(configDir, names)

	for _, name := range names {
		if !config.IsOAuthDetected(configDir, name) {
			t.Errorf("%s not marked", name)
		}
	}
}

func TestDetectImportedOAuthLeavesOpenServerUnmarked(t *testing.T) {
	configDir := t.TempDir()
	url := upstreamAnswering(t, http.StatusOK, "")
	names := importFromMCPJSON(t, configDir, `{"svc": {"type": "http", "url": "`+url+`"}}`)

	detect(configDir, names)

	if config.IsOAuthDetected(configDir, "svc") {
		t.Error("marker written for an upstream that did not demand OAuth")
	}
}

func TestDetectImportedOAuthProbesOnlyHTTPServersImportedThisRun(t *testing.T) {
	configDir := t.TempDir()
	url, requests := recordingUpstream(t)
	spawned := filepath.Join(t.TempDir(), "spawned")
	preexisting := importFromMCPJSON(t, configDir, `{"old": {"type": "http", "url": "`+url+`/old"}}`)
	names := importFromMCPJSON(t, configDir, `{
		"plain":    {"type": "http", "url": "`+url+`/plain"},
		"withkey":  {"type": "http", "url": "`+url+`/withkey", "headers": {"X-Api-Key": "k"}},
		"localcmd": {"command": "touch", "args": ["`+spawned+`"]}}`)
	if !slices.Equal(preexisting, []string{"old"}) || !slices.Equal(names, []string{"localcmd", "plain", "withkey"}) {
		t.Fatalf("unexpected imports: %v %v", preexisting, names)
	}

	detect(configDir, names)

	if got := requests.seen(); !slices.Equal(got, []string{"/plain", "/withkey"}) {
		t.Errorf("upstream saw requests for %v, want [/plain /withkey]", got)
	}
	if _, err := os.Stat(spawned); err == nil {
		t.Error("detection started the stdio server's command")
	}
}

func TestDetectImportedOAuthWithNoImportsStaysSilent(t *testing.T) {
	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "servers"), 0700); err != nil {
		t.Fatal(err)
	}
	writeImportSource(t, filepath.Join(configDir, "servers", "broken.yaml"), "bad: [yaml\n")
	errOut := &bytes.Buffer{}
	detectImportedOAuth(oauthDetectParams{configDir: configDir, clock: clock.System(), errOut: errOut})

	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
}

func TestDetectImportedOAuthProbesDespiteBrokenUnrelatedFile(t *testing.T) {
	configDir := t.TempDir()
	url := upstreamAnswering(t, http.StatusUnauthorized, "Bearer")
	names := importFromMCPJSON(t, configDir, `{"svc": {"type": "http", "url": "`+url+`"}}`)
	writeImportSource(t, filepath.Join(configDir, "servers", "broken.yaml"), "bad: [yaml\n")
	detect(configDir, names)
	if !config.IsOAuthDetected(configDir, "svc") {
		t.Error("OAuth marker not written despite unrelated broken server file")
	}
}

func TestDetectImportedOAuthGivesUpOnAnUnresponsiveServer(t *testing.T) {
	configDir := t.TempDir()
	names := importFromMCPJSON(t, configDir, `{"svc": {"type": "http", "url": "`+hangingUpstream(t)+`"}}`)
	fc := clock.NewFake()
	done := make(chan struct{})

	go func() {
		defer close(done)
		detectImportedOAuth(oauthDetectParams{configDir: configDir, names: names, clock: fc, errOut: &bytes.Buffer{}})
	}()
	waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := fc.BlockUntilContext(waitCtx, 1); err != nil {
		t.Fatal("probe deadline never started:", err)
	}
	fc.Advance(oauthProbeTimeout)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("detection still waiting on a server that never answers")
	}
	if config.IsOAuthDetected(configDir, "svc") {
		t.Error("an unresponsive server was marked as needing OAuth")
	}
}

func TestDetectImportedOAuthLeavesUnreachableServerUnmarked(t *testing.T) {
	configDir := t.TempDir()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	names := importFromMCPJSON(t, configDir, `{"svc": {"type": "http", "url": "`+closed.URL+`"}}`)

	detect(configDir, names)

	if config.IsOAuthDetected(configDir, "svc") {
		t.Error("marker written for an unreachable upstream")
	}
}

func TestInitCommandDetectsOAuthOnImportedServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	configDir := t.TempDir()
	url := upstreamAnswering(t, http.StatusUnauthorized, "Bearer")
	src := filepath.Join(t.TempDir(), "claude.json")
	writeImportSource(t, src, `{"mcpServers": {"svc": {"type": "http", "url": "`+url+`"}}}`)
	cmd := newInitCmd(&rootOptions{configDir: configDir})
	cmd.SetArgs([]string{"--yes", "--from", src})

	out := testutil.CaptureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(out, "svc (no token)") || !strings.Contains(out, "mini auth svc") {
		t.Errorf("init output = %q, want the login step to list svc", out)
	}
	if strings.Count(out, "imported") != 1 {
		t.Errorf("init output has %d imported lines, want one: %q", strings.Count(out, "imported"), out)
	}
}
