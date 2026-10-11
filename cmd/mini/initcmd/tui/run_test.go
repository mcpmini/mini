package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/testutil"
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
		m.View()
		for _, key := range keys {
			pressKey(m, key)
		}
		return nil
	}
}

// pressKey sends a key and draws the frame after it, as the program does: some screens move
// in the layout their last frame drew.
func pressKey(m tea.Model, key string) tea.Cmd {
	_, cmd := m.Update(press(key))
	m.View()
	return cmd
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
	_, next := m.Update(msg)
	deliver(m, next)
}

func noCatalog() (catalog.Catalog, error) { return catalog.Catalog{}, nil }

func setupFor(configDir string, list ...agents.Agent) initcmd.Setup {
	return initcmd.Setup{ConfigDir: configDir, Import: list}
}

// oauthEntry is a catalog server that declares OAuth, so writing it starts no network check.
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
	if err != nil || out.Saved {
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
		Program:     pressing("space", "tab", "enter"),
	})
	if err != nil || !out.Saved {
		t.Fatalf("Run = %+v, %v; want it finished and saved", out, err)
	}
	if got := writtenNames(t, configDir); !slices.Equal(got, []string{"notes"}) {
		t.Errorf("written = %v, want only notes: files was unticked", got)
	}
	if !slices.ContainsFunc(out.Report.Servers, func(s initcmd.ServerStatus) bool { return s.Name == "notes" }) {
		t.Errorf("report servers = %+v, want notes", out.Report.Servers)
	}
}

func TestRun_theSummarySkipsNothingTheImportScreenShowed(t *testing.T) {
	githubRunning := func(agent, command string) agents.Agent {
		entries := map[string]agents.Server{"github": {Config: config.ServerConfig{Command: command}}}
		return agents.Agent{Name: agent, ConfigPath: agent, Read: func(string) (map[string]agents.Server, error) {
			return entries, nil
		}}
	}
	out, err := Run(Params{
		Setup:       setupFor(t.TempDir(), githubRunning("Claude Code", "gh-one"), githubRunning("Codex", "gh-two")),
		LoadCatalog: noCatalog,
		Program:     pressing("tab", "enter"),
	})
	if err != nil || len(out.Report.Import.Candidates) < 2 {
		t.Fatalf("Run = %+v, %v; want github and an unticked github-2", out, err)
	}
	if skipped := out.Report.Import.Skipped; len(skipped) != 0 {
		t.Errorf("skipped = %+v; want none: the user saw github-2 on Import and left it unticked", skipped)
	}
}

func TestRun_goingBackToTickAnImportDropsTheSameCatalogServer(t *testing.T) {
	configDir := t.TempDir()
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("sentry")}}
	_, err := Run(Params{
		Setup:       setupFor(configDir, stdioAgent("Codex", "sentry")),
		LoadCatalog: fromCatalog(c),
		// Untick the import, tick the catalog's sentry, go back, tick the import again, finish.
		Program: pressing("space", "tab", "enter", "space", "esc", "up", "space", "tab", "enter", "enter"),
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
		// Tick linear and save; Logins lists it; back, swap linear for sentry, save again, continue.
		Program: pressing("space", "tab", "enter", "esc", "space", "down", "space", "tab", "enter", "down", "enter"),
	})
	if err != nil || !out.Saved {
		t.Fatalf("Run = %+v, %v; want it finished", out, err)
	}
	if got := writtenNames(t, configDir); !slices.Equal(got, []string{"sentry"}) {
		t.Errorf("written = %v, want sentry only: linear, written earlier in this run, was unticked", got)
	}
}

func TestRun_quittingAfterCatalogWritesNothing(t *testing.T) {
	configDir := t.TempDir()
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
	out, err := Run(
		Params{
			Setup:       setupFor(configDir),
			LoadCatalog: fromCatalog(c),
			Program:     pressing("space", "tab", "enter", "ctrl+c"),
		},
	)
	if err != nil || out.Saved {
		t.Fatalf("Run = %+v, %v; want a quit with nothing saved", out, err)
	}
	if got := writtenNames(t, configDir); len(got) > 0 {
		t.Errorf("written = %v, want nothing: linear was only staged", got)
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
	_, err := Run(
		Params{Setup: setupFor(configDir), LoadCatalog: fromCatalog(c), Program: pressing("space", "tab", "enter")},
	)
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
		if err != nil || !out.Saved || shown {
			t.Errorf("out = %+v, err = %v, UI shown = %v; want a finished run with no UI", out, err, shown)
		}
	})
	t.Run("the catalog is the first screen", func(t *testing.T) {
		configDir := t.TempDir()
		c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("sentry")}}
		_, err := Run(
			Params{Setup: setupFor(configDir), LoadCatalog: fromCatalog(c), Program: pressing("space", "tab", "enter")},
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
	if err != nil || !out.Saved {
		t.Errorf("Run = %+v, %v; want a finished run with a summary, not a quit", out, err)
	}
}

func blockUntilCancelled(ctx context.Context, _ string, _ config.ServerConfig) error {
	<-ctx.Done()
	return ctx.Err()
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
	t.Run("finishing waits for the running check", func(t *testing.T) {
		// The check ends only at its own timeout. Time in the bubble moves once Run blocks, so a
		// finish that waits sees the check done, and one that cancels it sees it unchecked.
		synctest.Test(t, func(t *testing.T) {
			setup := setupFor(t.TempDir())
			setup.Probe = blockUntilCancelled
			program := pressing("space", "tab", "enter", "enter")
			out, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(plain), Program: program})
			if err != nil || !out.Saved || statusOf(out.Report, "plain") != initcmd.Ready {
				t.Errorf("out = %+v, %v; want plain checked, so not marked as maybe needing a login", out, err)
			}
		})
	})
}

func TestRun_quittingCancelsAPendingLoginBeforeReturning(t *testing.T) {
	cancelled := false
	startLogin := func(ctx context.Context, _, _ string) (Login, error) {
		<-ctx.Done()
		cancelled = true
		return Login{}, ctx.Err()
	}
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
	out, err := Run(Params{
		Setup:       setupFor(t.TempDir()),
		LoadCatalog: fromCatalog(c),
		StartLogin:  startLogin,
		Program:     pressing("space", "tab", "enter", "enter", "ctrl+c"),
	})
	if err != nil || out.Saved {
		t.Fatalf("Run = %+v, %v; want a quit", out, err)
	}
	if !cancelled {
		t.Error("Run returned while linear's login still waited on the browser")
	}
}

func TestRun_aLoginDoesntOutliveTheServerItWasFor(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear"), oauthEntry("sentry")}}
	startLogin := func(context.Context, string, string) (Login, error) {
		return Login{URL: "https://auth.example/linear", Wait: func() error { return nil }}, nil
	}
	var screen string
	// Log in to linear; back, swap it for sentry, save; back, tick linear again, save.
	keys := []string{
		"space",
		"tab",
		"enter",
		"enter",
		"esc",
		"space",
		"down",
		"space",
		"tab",
		"enter",
		"esc",
		"up",
		"space",
		"tab",
		"enter",
	}
	program := func(m tea.Model) error {
		deliver(m, m.Init())
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m.View()
		for _, key := range keys {
			cmd := pressKey(m, key)
			deliver(m, cmd)
		}
		screen = shown(m.(*app))
		return nil
	}

	if _, err := Run(Params{
		Setup: setupFor(t.TempDir()), LoadCatalog: fromCatalog(c), StartLogin: startLogin, Program: program,
	}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(screen, "linear  needs a login") {
		t.Errorf("Logins:\n%s\nwant linear needing a login: unticking it deleted its credentials", screen)
	}
}

func TestRun_aLoginReachesMiniOnlyWhenTheRunFinishes(t *testing.T) {
	configDir := t.TempDir()
	token := filepath.Join("internal", "linear.token.json")
	if err := os.Mkdir(filepath.Join(configDir, "internal"), 0o700); err != nil {
		t.Fatal(err)
	}
	startLogin := func(_ context.Context, dir, _ string) (Login, error) {
		save := func() error {
			testutil.WriteFile(t, filepath.Join(dir, token), `{}`)
			return nil
		}
		return Login{URL: "https://auth.example/linear", Wait: save}, nil
	}
	var savedBeforeFinish bool
	program := func(m tea.Model) error {
		// Tick linear, leave Catalog, log in to linear.
		pressingAndDelivering("space", "tab", "enter", "enter")(m)
		_, err := os.Stat(filepath.Join(configDir, token))
		savedBeforeFinish = err == nil
		for _, key := range []string{"tab", "enter"} {
			deliver(m, pressKey(m, key))
		}
		return nil
	}
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
	out, err := Run(
		Params{Setup: setupFor(configDir), LoadCatalog: fromCatalog(c), StartLogin: startLogin, Program: program},
	)
	if err != nil || !out.Saved {
		t.Fatalf("Run = %+v, %v; want it finished", out, err)
	}
	if savedBeforeFinish {
		t.Error("linear's token was in mini before the run finished, want it staged")
	}
	if _, err := os.Stat(filepath.Join(configDir, token)); err != nil {
		t.Errorf("linear's token after finishing: %v, want it saved", err)
	}
}
