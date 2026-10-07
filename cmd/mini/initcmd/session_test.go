//go:build test

package initcmd

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/testutil"
)

func httpServer(name, url string) config.ServerConfig {
	return config.ServerConfig{Name: name, Transport: "http", URL: url}
}

type fakeProbe struct {
	blocksUntilCancelled bool
	started              chan string
	mu                   sync.Mutex
	probed               []string
	finished             int
}

func newFakeProbe(blocking bool) *fakeProbe {
	return &fakeProbe{blocksUntilCancelled: blocking, started: make(chan string, 10)}
}

func (f *fakeProbe) probe(ctx context.Context, _ string, sc config.ServerConfig) error {
	f.mu.Lock()
	f.probed = append(f.probed, sc.Name)
	f.mu.Unlock()
	f.started <- sc.Name
	if f.blocksUntilCancelled {
		<-ctx.Done()
	}
	f.mu.Lock()
	f.finished++
	f.mu.Unlock()
	return errors.New("unreachable")
}

func (f *fakeProbe) counts() (probed []string, finished int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.probed), f.finished
}

func newTestSession(t *testing.T, probe *fakeProbe, c clock.Clock) (*Session, string) {
	t.Helper()
	dir := t.TempDir()
	s := NewSession(SessionParams{ConfigDir: dir, Clock: c, Probe: probe.probe})
	t.Cleanup(s.Close)
	return s, dir
}

func waitStarted(t *testing.T, probe *fakeProbe) string {
	t.Helper()
	select {
	case name := <-probe.started:
		return name
	case <-time.After(5 * time.Second):
		t.Fatal("no OAuth check started")
		return ""
	}
}

func waitChanged(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Changed():
	case <-time.After(5 * time.Second):
		t.Fatal("no OAuth check state change")
	}
}

func TestSessionSync_isADeltaAgainstThisRun(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	kept := config.ServerConfig{Name: "kept", Command: "run"}
	s.Sync([]config.ServerConfig{httpServer("dropped", "https://dropped.example/mcp"), kept})
	keptPath := config.ServerPath(dir, "kept")
	marked := append(testutil.ReadFile(t, keptPath), "# untouched\n"...)
	testutil.WriteFileBytes(t, keptPath, marked)

	result := s.Sync([]config.ServerConfig{kept, httpServer("new", "https://new.example/mcp")})

	if !reflect.DeepEqual(result.Removed, []string{"dropped"}) || !reflect.DeepEqual(result.Added, []string{"new"}) ||
		result.Failed != nil {
		t.Errorf("result = %+v, want dropped removed and new added", result)
	}
	if config.ServerFileExists(dir, "dropped") {
		t.Error("the unticked server's file is still there")
	}
	if got := testutil.ReadFile(t, keptPath); string(got) != string(marked) {
		t.Errorf("the kept server's file was rewritten:\n%s", got)
	}
}

func TestSessionSync_aChangedSourceIsWrittenAgain(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	s.Sync([]config.ServerConfig{httpServer("github", "https://catalog.example/mcp")})

	result := s.Sync([]config.ServerConfig{httpServer("github", "https://imported.example/mcp")})

	if !reflect.DeepEqual(result.Removed, []string{"github"}) || !reflect.DeepEqual(result.Added, []string{"github"}) {
		t.Errorf("result = %+v, want github removed and added again", result)
	}
	if got := string(
		testutil.ReadFile(t, config.ServerPath(dir, "github")),
	); !strings.Contains(
		got,
		"imported.example",
	) {
		t.Errorf("server file = %s, want the imported URL", got)
	}
}

func TestSessionChecks_aChangedServerIsCheckedAgain(t *testing.T) {
	probe := newFakeProbe(false)
	s, _ := newTestSession(t, probe, clock.System())
	s.Sync([]config.ServerConfig{httpServer("github", "https://catalog.example/mcp")})
	waitStarted(t, probe)
	for s.Running()["github"] {
		waitChanged(t, s)
	}

	s.Sync([]config.ServerConfig{httpServer("github", "https://imported.example/mcp")})
	s.Close()

	if probed, _ := probe.counts(); !reflect.DeepEqual(probed, []string{"github", "github"}) {
		t.Errorf("probed = %v, want github checked again after its URL changed", probed)
	}
}

func TestSessionSync_aChangeMadeThroughSharedMapsIsStillWritten(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	github := httpServer("github", "https://gh.example/mcp")
	github.Headers = map[string]string{"X-Team": "first"}
	s.Sync([]config.ServerConfig{github})

	github.Headers["X-Team"] = "second"
	result := s.Sync([]config.ServerConfig{github})

	if !reflect.DeepEqual(result.Added, []string{"github"}) {
		t.Errorf("result = %+v, want github written again", result)
	}
	if got := string(testutil.ReadFile(t, config.ServerPath(dir, "github"))); !strings.Contains(got, "second") {
		t.Errorf("server file = %s, want the changed header", got)
	}
}

func TestSessionSync_aRejectedReplacementKeepsTheWrittenServer(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	s.Sync([]config.ServerConfig{{Name: "svc", Command: "run"}})
	before := testutil.ReadFile(t, config.ServerPath(dir, "svc"))

	result := s.Sync([]config.ServerConfig{{Name: "svc", Command: "run", HandshakeTimeout: "nonsense"}})

	if len(result.Failed) != 1 || result.Failed[0].Name != "svc" || result.Removed != nil {
		t.Errorf("result = %+v, want svc reported as failed and nothing removed", result)
	}
	if after := testutil.ReadFile(t, config.ServerPath(dir, "svc")); string(after) != string(before) {
		t.Errorf("server file = %s, want the previous config kept", after)
	}
}

func TestSessionSync_neverRemovesServersFromBeforeTheRun(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "existing", Command: "run"})

	result := s.Sync([]config.ServerConfig{{Name: "existing", Command: "other"}})
	if len(result.Failed) != 1 || !errors.Is(result.Failed[0].Err, ops.ErrAlreadyConfigured) {
		t.Errorf("failed = %+v, want existing reported as already configured", result.Failed)
	}
	s.Sync(nil)
	if !config.ServerFileExists(dir, "existing") {
		t.Error("a server configured before the run was removed")
	}
}

func TestSessionChecks_onlyWrittenHTTPServersWithoutCredentials(t *testing.T) {
	probe := newFakeProbe(false)
	s, dir := newTestSession(t, probe, clock.System())
	configtest.WriteServer(t, dir, httpServer("existing", "https://existing.example/mcp"))
	withHeader := httpServer("keyed", "https://keyed.example/mcp")
	withHeader.Headers = map[string]string{"X-Api-Key": "${KEY}"}

	s.Sync(
		[]config.ServerConfig{
			httpServer("open", "https://open.example/mcp"),
			withHeader,
			{Name: "local", Command: "run"},
		},
	)
	s.Close()

	if probed, _ := probe.counts(); !reflect.DeepEqual(probed, []string{"open"}) {
		t.Errorf("probed = %v, want only open", probed)
	}
}

func TestSessionChecks_aBrokenUnrelatedServerFileDoesNotStopTheChecks(t *testing.T) {
	probe := newFakeProbe(false)
	s, dir := newTestSession(t, probe, clock.System())
	testutil.WriteFile(t, config.ServerPath(dir, "broken"), "transport: [unclosed\n")

	s.Sync([]config.ServerConfig{httpServer("open", "https://open.example/mcp")})
	s.Close()

	if probed, _ := probe.counts(); !reflect.DeepEqual(probed, []string{"open"}) {
		t.Errorf("probed = %v, want open checked despite the broken servers/broken.yaml", probed)
	}
}

func TestSessionChecks_resyncCancelsWaitsAndChecksAgain(t *testing.T) {
	probe := newFakeProbe(true)
	s, _ := newTestSession(t, probe, clock.System())
	open := httpServer("open", "https://open.example/mcp")
	s.Sync([]config.ServerConfig{open})
	waitStarted(t, probe)
	if !s.Running()["open"] {
		t.Fatal("Checking = false while the check runs")
	}

	s.Sync([]config.ServerConfig{open})

	if _, finished := probe.counts(); finished != 1 {
		t.Errorf("Sync returned with %d checks finished, want the cancelled one finished", finished)
	}
	waitStarted(t, probe)
	if !s.Running()["open"] {
		t.Error("the cancelled check wasn't started again")
	}
}

func TestSessionChecks_aTimedOutCheckIsDone(t *testing.T) {
	probe, fake := newFakeProbe(true), clock.NewFake()
	s, _ := newTestSession(t, probe, fake)
	open := httpServer("open", "https://open.example/mcp")
	s.Sync([]config.ServerConfig{open})
	waitChanged(t, s)
	waitStarted(t, probe)
	if err := fake.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal(err)
	}

	fake.Advance(oauthCheckTimeout)
	waitChanged(t, s)

	if s.Running()["open"] {
		t.Error("Checking = true after the timeout")
	}
	s.Sync([]config.ServerConfig{open})
	s.Close()
	if probed, _ := probe.counts(); len(probed) != 1 {
		t.Errorf("probed = %v, want the finished check not repeated", probed)
	}
}

func TestSessionClose_cancelsAndWaitsForChecks(t *testing.T) {
	probe := newFakeProbe(true)
	s, _ := newTestSession(t, probe, clock.System())
	s.Sync([]config.ServerConfig{httpServer("open", "https://open.example/mcp")})
	waitStarted(t, probe)

	s.Close()

	if _, finished := probe.counts(); finished != 1 || s.Running()["open"] {
		t.Errorf("after Close: finished = %d, checking = %v; want the check finished", finished, s.Running()["open"])
	}
}

func TestSessionUnchecked_namesTheChecksThatNeverFinished(t *testing.T) {
	probe := newFakeProbe(true)
	s, _ := newTestSession(t, probe, clock.System())
	s.Sync([]config.ServerConfig{httpServer("open", "https://open.example/mcp"), {Name: "local", Command: "run"}})
	waitStarted(t, probe)
	if running := s.Running(); !running["open"] || len(running) != 1 {
		t.Errorf("Running = %v, want open only", running)
	}

	s.Close()

	if got := s.Unchecked(); !reflect.DeepEqual(got, []string{"open"}) {
		t.Errorf("Unchecked after Close = %v, want open: its check was cancelled", got)
	}
}
