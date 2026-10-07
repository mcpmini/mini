package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/ops"
)

func stdioAgent(name string, servers ...string) agents.Agent {
	entries := map[string]agents.Server{}
	for _, server := range servers {
		entries[server] = agents.Server{Config: config.ServerConfig{Command: server + "-server"}}
	}
	return agents.Agent{Name: name, ConfigPath: name, Read: func(string) (map[string]agents.Server, error) {
		return entries, nil
	}}
}

// pressing runs the background loads to completion first, as if they finished before the first key.
func pressing(keys ...string) func(tea.Model) error {
	return func(m tea.Model) error {
		deliver(m, m.Init())
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		for _, key := range keys {
			m.Update(press(key))
		}
		return nil
	}
}

func deliver(m tea.Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			deliver(m, c)
		}
		return
	}
	m.Update(msg)
}

func noCatalog() (catalog.Catalog, error) { return catalog.Catalog{}, nil }

func setupFor(configDir string, list ...agents.Agent) initcmd.Setup {
	return initcmd.Setup{ConfigDir: configDir, Import: list}
}

// oauthEntry is a catalog server with bundled OAuth, so writing it starts no network check.
func oauthEntry(name string) catalog.Entry {
	return catalog.Entry{Name: name, URL: "https://mcp." + name + ".example/mcp", Auth: catalog.AuthOAuth2}
}

func writtenNames(t *testing.T, configDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(configDir, "servers"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	return names
}

func TestRun_quittingBeforeCatalogWritesNothing(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	out, err := Run(Params{
		Setup:       setupFor(configDir, stdioAgent("Codex", "files")),
		LoadCatalog: noCatalog,
		Program:     pressing("ctrl+c"),
	})
	if err != nil || !out.Quit || out.Saved {
		t.Fatalf("Run = %+v, %v; want a quit with nothing saved", out, err)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("config dir after quitting: %v, want it untouched", err)
	}
}

func TestRun_finishingWritesTheTicksAndReportsThem(t *testing.T) {
	configDir := t.TempDir()
	out, err := Run(Params{
		Setup:       setupFor(configDir, stdioAgent("Codex", "files", "notes")),
		LoadCatalog: noCatalog,
		Program:     pressing("space", "enter"),
	})
	if err != nil || out.Quit || !out.Saved {
		t.Fatalf("Run = %+v, %v; want it finished and saved", out, err)
	}
	if got := writtenNames(t, configDir); !slices.Equal(got, []string{"notes"}) {
		t.Errorf("written = %v, want only notes: files was unticked", got)
	}
	if !slices.ContainsFunc(out.Report.Servers, func(s initcmd.ServerStatus) bool { return s.Name == "notes" }) {
		t.Errorf("report servers = %+v, want notes", out.Report.Servers)
	}
}

func TestRun_goingBackToTickAnImportDropsTheSameCatalogServer(t *testing.T) {
	configDir := t.TempDir()
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("sentry")}}
	_, err := Run(Params{
		Setup:       setupFor(configDir, stdioAgent("Codex", "sentry")),
		LoadCatalog: fromCatalog(c),
		// Untick the import, tick the catalog's sentry, go back, tick the import again, finish.
		Program: pressing("space", "enter", "space", "esc", "space", "enter", "enter"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sentry, err := config.ReadUnexpandedServer(configDir, "sentry")
	if err != nil || sentry.Command != "sentry-server" {
		t.Errorf("sentry = %+v, %v; want the imported stdio server, not the catalog's", sentry, err)
	}
}

func TestRun_goingBackFromLoginsSyncsAgain(t *testing.T) {
	configDir := t.TempDir()
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear"), oauthEntry("sentry")}}
	out, err := Run(Params{
		Setup:       setupFor(configDir),
		LoadCatalog: fromCatalog(c),
		// Tick linear and save; Logins lists it; back, swap linear for sentry, save again, finish.
		Program: pressing("space", "enter", "esc", "space", "down", "space", "enter", "enter"),
	})
	if err != nil || out.Quit {
		t.Fatalf("Run = %+v, %v; want it finished", out, err)
	}
	if got := writtenNames(t, configDir); !slices.Equal(got, []string{"sentry"}) {
		t.Errorf("written = %v, want sentry only: linear, written earlier in this run, was unticked", got)
	}
}

func TestRun_quittingAfterCatalogKeepsWhatWasWritten(t *testing.T) {
	configDir := t.TempDir()
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
	out, err := Run(
		Params{Setup: setupFor(configDir), LoadCatalog: fromCatalog(c), Program: pressing("space", "enter", "ctrl+c")},
	)
	if err != nil || !out.Quit || !out.Saved {
		t.Fatalf("Run = %+v, %v; want a quit after saving", out, err)
	}
	if got := writtenNames(t, configDir); !slices.Equal(got, []string{"linear"}) {
		t.Errorf("written = %v, want linear kept", got)
	}
}

func TestRun_theCatalogOffersOnlyServersMiniHasNot(t *testing.T) {
	configDir := t.TempDir()
	if _, err := ops.AddServer(
		configDir,
		config.ServerConfig{Name: "linear", Transport: "http", URL: "https://mcp.linear.example/mcp"},
	); err != nil {
		t.Fatal(err)
	}
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear"), oauthEntry("sentry")}}
	_, err := Run(Params{Setup: setupFor(configDir), LoadCatalog: fromCatalog(c), Program: pressing("space", "enter")})
	if got := writtenNames(t, configDir); err != nil || !slices.Equal(got, []string{"linear", "sentry"}) {
		t.Errorf(
			"written = %v, err = %v; want sentry added: linear is configured, so the first row is sentry",
			got,
			err,
		)
	}
}

func TestRun_withNothingToImport(t *testing.T) {
	t.Run("and an empty catalog, no UI is shown", func(t *testing.T) {
		shown := false
		program := func(tea.Model) error {
			shown = true
			return nil
		}
		out, err := Run(Params{Setup: setupFor(t.TempDir()), LoadCatalog: noCatalog, Program: program})
		if err != nil || out.Quit || shown {
			t.Errorf("out = %+v, err = %v, UI shown = %v; want a finished run with no UI", out, err, shown)
		}
	})
	t.Run("the catalog is the first screen", func(t *testing.T) {
		configDir := t.TempDir()
		c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("sentry")}}
		_, err := Run(
			Params{Setup: setupFor(configDir), LoadCatalog: fromCatalog(c), Program: pressing("space", "enter")},
		)
		if got := writtenNames(t, configDir); err != nil || !slices.Equal(got, []string{"sentry"}) {
			t.Errorf("written = %v, err = %v; want sentry ticked on the catalog shown first", got, err)
		}
	})
}

func TestRun_aRunThatStartsOnLoginsFinishesWithItsSummary(t *testing.T) {
	configDir := t.TempDir()
	configtest.WriteServer(
		t,
		configDir,
		config.ServerConfig{Name: "files", Command: "run", Env: []string{"ROOT=${MINI_TEST_UNSET_ROOT}"}},
	)
	out, err := Run(Params{Setup: setupFor(configDir), LoadCatalog: noCatalog, Program: pressing("enter")})
	if err != nil || out.Quit || !out.Saved {
		t.Errorf("Run = %+v, %v; want a finished run with a summary, not a quit", out, err)
	}
}

// oauthChallenge stands in for a server that answers with a 401 once release is closed. Like a
// real probe, a check cancelled before that records nothing.
type oauthChallenge struct {
	release chan struct{}
}

func (c oauthChallenge) probe(ctx context.Context, configDir string, sc config.ServerConfig) error {
	select {
	case <-c.release:
		return config.MarkOAuthDetected(configDir, sc.Name)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func statusOf(report initcmd.Report, name string) initcmd.Readiness {
	i := slices.IndexFunc(report.Servers, func(s initcmd.ServerStatus) bool { return s.Name == name })
	if i < 0 {
		return -1
	}
	return report.Servers[i].Readiness
}

func TestRun_theSummaryIsReadOnlyOnceNoCheckIsRunning(t *testing.T) {
	plain := catalog.Catalog{Entries: []catalog.Entry{{Name: "plain", URL: "https://mcp.plain.example/mcp"}}}
	t.Run("quitting cancels the running check, and the summary says it wasn't checked", func(t *testing.T) {
		configDir := t.TempDir()
		setup := setupFor(configDir)
		setup.Probe = oauthChallenge{release: make(chan struct{})}.probe
		var view string
		program := func(m tea.Model) error {
			pressing("space", "enter")(m)
			view = ansi.Strip(m.(*app).render())
			m.Update(press("ctrl+c"))
			return nil
		}
		out, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(plain), Program: program})
		if !strings.Contains(view, "plain  checking…") {
			t.Errorf("Logins while the check ran:\n%s\nwant plain checking", view)
		}
		if err != nil || !out.Quit || statusOf(out.Report, "plain") != initcmd.MayNeedLogin {
			t.Errorf("out = %+v, %v; want plain marked as maybe needing a login, not set up", out, err)
		}
	})
	t.Run("finishing waits for the running check", func(t *testing.T) {
		configDir := t.TempDir()
		challenge := oauthChallenge{release: make(chan struct{})}
		setup := setupFor(configDir)
		setup.Probe = challenge.probe
		program := func(m tea.Model) error {
			pressing("space", "enter", "enter")(m)
			close(challenge.release)
			return nil
		}
		out, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(plain), Program: program})
		if err != nil || out.Quit || statusOf(out.Report, "plain") != initcmd.NeedsLogin {
			t.Errorf("out = %+v, %v; want the summary read after the check recorded OAuth", out, err)
		}
	})
}
