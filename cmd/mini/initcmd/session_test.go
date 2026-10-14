//go:build test

package initcmd

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
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

func newTestSession(t *testing.T, probe *fakeProbe, c clock.Clock) (*session, string) {
	t.Helper()
	dir := t.TempDir()
	s := newSession(sessionParams{ConfigDir: dir, Clock: c, Probe: probe.probe})
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

func waitChanged(t *testing.T, s *session) {
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
	s.Sync([]config.ServerConfig{httpServer("dropped", "https://dropped.example/mcp"), kept}, nil)
	keptPath := config.ServerPath(dir, "kept")
	marked := append(testutil.ReadFile(t, keptPath), "# untouched\n"...)
	testutil.WriteFileBytes(t, keptPath, marked)

	result := s.Sync([]config.ServerConfig{kept, httpServer("new", "https://new.example/mcp")}, nil)

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
	s.Sync([]config.ServerConfig{httpServer("github", "https://catalog.example/mcp")}, nil)

	result := s.Sync([]config.ServerConfig{httpServer("github", "https://imported.example/mcp")}, nil)

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

func TestSessionSync_aServerWhoseSetupCommentChangedIsWrittenAgain(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	github := httpServer("github", "https://github.example/mcp")
	s.Sync([]config.ServerConfig{github}, nil)

	s.Sync([]config.ServerConfig{github}, map[string]string{"github": "add your token"})

	if got := string(
		testutil.ReadFile(t, config.ServerPath(dir, "github")),
	); !strings.Contains(
		got,
		"# add your token",
	) {
		t.Errorf("github.yaml:\n%s\nwant the setup comment the second save asked for", got)
	}
}

func TestSessionChecks_aChangedServerIsCheckedAgain(t *testing.T) {
	probe := newFakeProbe(false)
	s, _ := newTestSession(t, probe, clock.System())
	s.Sync([]config.ServerConfig{httpServer("github", "https://catalog.example/mcp")}, nil)
	waitStarted(t, probe)
	for s.Running()["github"] {
		waitChanged(t, s)
	}

	s.Sync([]config.ServerConfig{httpServer("github", "https://imported.example/mcp")}, nil)
	s.Close()

	if probed, _ := probe.counts(); !reflect.DeepEqual(probed, []string{"github", "github"}) {
		t.Errorf("probed = %v, want github checked again after its URL changed", probed)
	}
}

func TestSessionSync_aChangeMadeThroughSharedMapsIsStillWritten(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	github := httpServer("github", "https://gh.example/mcp")
	github.Headers = map[string]string{"X-Team": "first"}
	s.Sync([]config.ServerConfig{github}, nil)

	github.Headers["X-Team"] = "second"
	result := s.Sync([]config.ServerConfig{github}, nil)

	if !reflect.DeepEqual(result.Added, []string{"github"}) {
		t.Errorf("result = %+v, want github written again", result)
	}
	if got := string(testutil.ReadFile(t, config.ServerPath(dir, "github"))); !strings.Contains(got, "second") {
		t.Errorf("server file = %s, want the changed header", got)
	}
}

func TestSessionSync_aRejectedReplacementKeepsTheWrittenServer(t *testing.T) {
	s, dir := newTestSession(t, newFakeProbe(false), clock.System())
	s.Sync([]config.ServerConfig{{Name: "svc", Command: "run"}}, nil)
	before := testutil.ReadFile(t, config.ServerPath(dir, "svc"))

	result := s.Sync([]config.ServerConfig{{Name: "svc", Command: "run", HandshakeTimeout: "nonsense"}}, nil)

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

	result := s.Sync([]config.ServerConfig{{Name: "existing", Command: "other"}}, nil)
	if len(result.Failed) != 1 || !errors.Is(result.Failed[0].Err, ops.ErrAlreadyConfigured) {
		t.Errorf("failed = %+v, want existing reported as already configured", result.Failed)
	}
	s.Sync(nil, nil)
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
		nil,
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

	s.Sync([]config.ServerConfig{httpServer("open", "https://open.example/mcp")}, nil)
	s.Close()

	if probed, _ := probe.counts(); !reflect.DeepEqual(probed, []string{"open"}) {
		t.Errorf("probed = %v, want open checked despite the broken servers/broken.yaml", probed)
	}
}

func TestSessionChecks_resyncCancelsWaitsAndChecksAgain(t *testing.T) {
	probe := newFakeProbe(true)
	s, _ := newTestSession(t, probe, clock.System())
	open := httpServer("open", "https://open.example/mcp")
	s.Sync([]config.ServerConfig{open}, nil)
	waitStarted(t, probe)
	if !s.Running()["open"] {
		t.Fatal("Checking = false while the check runs")
	}

	s.Sync([]config.ServerConfig{open}, nil)

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
	s.Sync([]config.ServerConfig{open}, nil)
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
	s.Sync([]config.ServerConfig{open}, nil)
	s.Close()
	if probed, _ := probe.counts(); len(probed) != 1 {
		t.Errorf("probed = %v, want the finished check not repeated", probed)
	}
}

func TestSessionClose_cancelsAndWaitsForChecks(t *testing.T) {
	probe := newFakeProbe(true)
	s, _ := newTestSession(t, probe, clock.System())
	s.Sync([]config.ServerConfig{httpServer("open", "https://open.example/mcp")}, nil)
	waitStarted(t, probe)

	s.Close()

	if _, finished := probe.counts(); finished != 1 || s.Running()["open"] {
		t.Errorf("after Close: finished = %d, checking = %v; want the check finished", finished, s.Running()["open"])
	}
}

// fakeDiscovery answers each server URL with its authorization server's metadata, or an error.
type fakeDiscovery struct {
	mu         sync.Mutex
	metas      map[string]*auth.ServerMeta
	discovered []string
}

func (f *fakeDiscovery) discover(_ context.Context, serverURL string) (*auth.ServerMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discovered = append(f.discovered, serverURL)
	if meta, ok := f.metas[serverURL]; ok {
		return meta, nil
	}
	return nil, errors.New("discovery failed")
}

func oauthServer(name, clientID string) config.ServerConfig {
	sc := httpServer(name, "https://"+name+".example/mcp")
	sc.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: clientID}
	return sc
}

func customCallbackPort(sc config.ServerConfig) config.ServerConfig {
	sc.Auth.CallbackPort = 7777
	return sc
}

func TestSessionChecks_discoveryNamesTheLoginsThatNeedTheUsersOwnApp(t *testing.T) {
	stage, miniDir := t.TempDir(), t.TempDir()
	for _, sc := range []config.ServerConfig{oauthServer("asana", ""), oauthServer("own", "my-app"), oauthServer("box", "")} {
		configtest.WriteServer(t, miniDir, sc)
	}
	if err := auth.SaveRegistration(miniDir, "box", &auth.Registration{
		Issuer: "https://box.example", ClientID: "registered-earlier",
	}); err != nil {
		t.Fatal(err)
	}
	discovery := &fakeDiscovery{metas: map[string]*auth.ServerMeta{
		"https://asana.example/mcp":  {Issuer: "https://asana.example"},
		"https://github.example/mcp": {Issuer: "https://github.example"},
		"https://box.example/mcp":    {Issuer: "https://box.example"},
		"https://linear.example/mcp": {
			Issuer: "https://linear.example", RegistrationURL: "https://linear.example/register",
		},
		"https://notion.example/mcp": {Issuer: "https://notion.example", CIMDSupported: true},
		"https://sentry.example/mcp": {Issuer: "https://sentry.example", CIMDSupported: true},
		"https://pinned.example/mcp": {Issuer: "https://pinned.example", CIMDSupported: true},
	}}
	var probed []string
	var mu sync.Mutex
	probe := func(_ context.Context, dir string, sc config.ServerConfig) error {
		mu.Lock()
		probed = append(probed, sc.Name)
		mu.Unlock()
		if sc.Name == "keyed" {
			return fmt.Errorf("connect: %w", &transport.UnauthorizedError{WWWAuthenticate: "ApiKey"})
		}
		return config.MarkOAuthDetected(dir, sc.Name)
	}
	s := newSession(sessionParams{ConfigDir: stage, MiniDir: miniDir, Probe: probe, Discover: discovery.discover})
	t.Cleanup(s.Close)

	s.Sync([]config.ServerConfig{
		httpServer("github", "https://github.example/mcp"),
		httpServer("linear", "https://linear.example/mcp"),
		httpServer("notion", "https://notion.example/mcp"),
		httpServer("flaky", "https://flaky.example/mcp"),
		httpServer("keyed", "https://keyed.example/mcp"),
		oauthServer("sentry", ""),
		customCallbackPort(oauthServer("pinned", "")),
	}, nil)
	s.WaitChecks()

	findings := s.Findings()
	want := map[string]bool{"asana": true, "github": true, "pinned": true}
	if got := findings.NoSelfRegistration; !reflect.DeepEqual(got, want) {
		t.Errorf("no self-registration = %v, want asana and github, which offer neither DCR nor CIMD, and pinned, "+
			"whose callback port mini's metadata document doesn't list; flaky's failed discovery proves nothing", got)
	}
	if got := findings.NeedsCredential; !reflect.DeepEqual(got, map[string]bool{"keyed": true}) {
		t.Errorf("needs credential = %v, want keyed: it refused the probe without an OAuth challenge", got)
	}
	slices.Sort(probed)
	if !slices.Equal(probed, []string{"flaky", "github", "keyed", "linear", "notion"}) {
		t.Errorf("probed = %v, want this run's servers of unknown auth: asana was the user's before, "+
			"and sentry already declares OAuth", probed)
	}
	if !slices.Contains(discovery.discovered, "https://sentry.example/mcp") {
		t.Error("sentry's authorization server wasn't discovered, want it checked without a probe")
	}
	if slices.Contains(discovery.discovered, "https://own.example/mcp") {
		t.Error("discovered configured own-client server")
	}
	if !slices.Contains(discovery.discovered, "https://box.example/mcp") {
		t.Error("issuer discovery was skipped for the stored registration")
	}
}

func refusesWithoutOAuth(context.Context, string, config.ServerConfig) error {
	return fmt.Errorf("connect: %w", &transport.UnauthorizedError{WWWAuthenticate: "ApiKey"})
}

func TestSessionChecks_aServerRefusingWithoutOAuthNeedsACredential(t *testing.T) {
	t.Run("one already in mini is probed too, its file untouched", func(t *testing.T) {
		stage, miniDir := t.TempDir(), t.TempDir()
		configtest.WriteServer(t, miniDir, httpServer("keyed", "https://keyed.example/mcp"))
		before := testutil.ReadFile(t, config.ServerPath(miniDir, "keyed"))
		s := newSession(sessionParams{
			ConfigDir: stage, MiniDir: miniDir, Probe: refusesWithoutOAuth, Discover: (&fakeDiscovery{}).discover,
		})
		t.Cleanup(s.Close)
		s.Sync(nil, nil)
		s.WaitChecks()
		if got := s.Findings().NeedsCredential; !reflect.DeepEqual(got, map[string]bool{"keyed": true}) {
			t.Errorf("needs credential = %v, want keyed, which the user added before this run", got)
		}
		if after := testutil.ReadFile(t, config.ServerPath(miniDir, "keyed")); string(after) != string(before) {
			t.Errorf("keyed.yaml changed:\n%s", after)
		}
	})
	t.Run("a server rewritten so it no longer refuses loses the mark", func(t *testing.T) {
		var refuse sync.Map
		refuse.Store("https://old.example/mcp", true)
		probe := func(ctx context.Context, dir string, sc config.ServerConfig) error {
			if _, ok := refuse.Load(sc.URL); ok {
				return refusesWithoutOAuth(ctx, dir, sc)
			}
			return nil
		}
		s := newSession(sessionParams{ConfigDir: t.TempDir(), Probe: probe, Discover: (&fakeDiscovery{}).discover})
		t.Cleanup(s.Close)
		s.Sync([]config.ServerConfig{httpServer("files", "https://old.example/mcp")}, nil)
		s.WaitChecks()
		s.Sync([]config.ServerConfig{httpServer("files", "https://new.example/mcp")}, nil)
		s.WaitChecks()
		if got := s.Findings().NeedsCredential; len(got) > 0 {
			t.Errorf("needs credential = %v, want none: the rewritten files answered its probe", got)
		}
	})
	t.Run("a probe cut short proves nothing", func(t *testing.T) {
		probe := func(ctx context.Context, dir string, sc config.ServerConfig) error {
			<-ctx.Done()
			return refusesWithoutOAuth(ctx, dir, sc)
		}
		s := newSession(sessionParams{ConfigDir: t.TempDir(), Probe: probe, Discover: (&fakeDiscovery{}).discover})
		s.Sync([]config.ServerConfig{httpServer("slow", "https://slow.example/mcp")}, nil)
		s.Close()
		if got := s.Findings().NeedsCredential; len(got) > 0 {
			t.Errorf("needs credential = %v, want none: the 401 came before the probe could look for OAuth", got)
		}
	})
}
