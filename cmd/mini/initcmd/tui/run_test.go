package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
		Program:     pressing("enter"),
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
		// Tick linear and save; Logins lists it; back, swap linear for sentry, save again, continue.
		Program: pressing("space", "enter", "esc", "space", "down", "space", "enter", "down", "enter"),
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
	t.Run("quitting cancels the running check, and the summary says it wasn't checked", func(t *testing.T) {
		configDir := t.TempDir()
		setup := setupFor(configDir)
		setup.Probe = blockUntilCancelled
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
		// The check ends only at its own timeout. Time in the bubble moves once Run blocks, so a
		// finish that waits sees the check done, and one that cancels it sees it unchecked.
		synctest.Test(t, func(t *testing.T) {
			setup := setupFor(t.TempDir())
			setup.Probe = blockUntilCancelled
			program := pressing("space", "enter", "enter")
			out, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(plain), Program: program})
			if err != nil || out.Quit || statusOf(out.Report, "plain") != initcmd.Ready {
				t.Errorf("out = %+v, %v; want plain checked, so not marked as maybe needing a login", out, err)
			}
		})
	})
}

func TestRun_quittingCancelsAPendingLoginBeforeReturning(t *testing.T) {
	cancelled := false
	startLogin := func(ctx context.Context, _ string) (Login, error) {
		<-ctx.Done()
		cancelled = true
		return Login{}, ctx.Err()
	}
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
	out, err := Run(Params{
		Setup:       setupFor(t.TempDir()),
		LoadCatalog: fromCatalog(c),
		StartLogin:  startLogin,
		Program:     pressing("space", "enter", "enter", "ctrl+c"),
	})
	if err != nil || !out.Quit {
		t.Fatalf("Run = %+v, %v; want a quit", out, err)
	}
	if !cancelled {
		t.Error("Run returned while linear's login still waited on the browser")
	}
}

func claudeWithServers(t *testing.T) agents.Agent {
	t.Helper()
	home := t.TempDir()
	claude := agents.Known(home)[0]
	testutil.WriteFile(t, claude.ConfigPath, `{"mcpServers":{"files":{"command":"files-server"}}}`)
	return claude
}

func TestRun_connect(t *testing.T) {
	run := func(t *testing.T, claude agents.Agent, keys ...string) Outcome {
		t.Helper()
		setup := setupFor(t.TempDir())
		setup.AgentsToConnect = []agents.Agent{claude}
		// Catalog comes first and is left with enter, which saves; then Connect is shown.
		c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
		out, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(c), Program: pressing(keys...)})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	t.Run("just connect adds mini to the agent and reports it", func(t *testing.T) {
		claude := claudeWithServers(t)
		out := run(t, claude, "enter", "enter")
		config := string(testutil.ReadFile(t, claude.ConfigPath))
		if !strings.Contains(config, `"mini"`) || !strings.Contains(config, `"files"`) {
			t.Errorf("agent config = %s, want mini added next to files", config)
		}
		if len(out.Report.Connected) != 1 || out.Report.Connected[0].Backup == "" {
			t.Errorf("connected = %+v, want Claude Code connected with a backup", out.Report.Connected)
		}
	})
	t.Run("an agent with a mini entry already isn't offered", func(t *testing.T) {
		claude := claudeWithServers(t)
		testutil.WriteFile(t, claude.ConfigPath, `{"mcpServers":{"mini":{"command":"mini","args":["connect"]}}}`)
		setup := setupFor(t.TempDir())
		setup.AgentsToConnect = []agents.Agent{claude}
		var view string
		program := func(m tea.Model) error {
			pressing("enter")(m)
			view = ansi.Strip(m.(*app).render())
			return nil
		}
		c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
		if _, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(c), Program: program}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(view, "Connect mini") {
			t.Errorf("screen after Catalog:\n%s\nwant Connect skipped: Claude Code has a mini entry", view)
		}
	})
	for name, keys := range map[string][]string{
		"don't connect leaves the agent as it was":            {"enter", "down", "enter"},
		"quitting on Connect after the save leaves the agent": {"enter", "ctrl+c"},
		"a ctrl+c right after choosing leaves the agent":      {"enter", "enter", "ctrl+c"},
	} {
		t.Run(name, func(t *testing.T) {
			claude := claudeWithServers(t)
			before := testutil.ReadFile(t, claude.ConfigPath)
			out := run(t, claude, keys...)
			after := testutil.ReadFile(t, claude.ConfigPath)
			if string(after) != string(before) || out.Report.Connected != nil {
				t.Errorf("agent config = %s, connected = %+v; want both untouched", after, out.Report.Connected)
			}
		})
	}
}

// pressingAndDelivering also runs the command each key returns, as the program would, so
// background work a screen starts finishes before the next key.
func pressingAndDelivering(keys ...string) func(tea.Model) error {
	return func(m tea.Model) error {
		deliver(m, m.Init())
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		for _, key := range keys {
			_, cmd := m.Update(press(key))
			deliver(m, cmd)
		}
		return nil
	}
}

func TestRun_connectAndRemove(t *testing.T) {
	run := func(t *testing.T, check error) (agents.Agent, Outcome) {
		t.Helper()
		configDir := t.TempDir()
		configtest.WriteServer(t, configDir, config.ServerConfig{Name: "files", Command: "files-server"})
		claude := claudeWithServers(t)
		setup := setupFor(configDir)
		setup.AgentsToConnect = []agents.Agent{claude}
		setup.Probe = func(context.Context, string, config.ServerConfig) error { return check }
		// Nothing to import or add, so Connect is the first screen; enter picks the first option.
		out, err := Run(Params{Setup: setup, LoadCatalog: noCatalog, Program: pressingAndDelivering("enter")})
		if err != nil {
			t.Fatal(err)
		}
		return claude, out
	}
	t.Run("an entry whose mini copy passed its check is replaced by mini", func(t *testing.T) {
		claude, out := run(t, nil)
		config := string(testutil.ReadFile(t, claude.ConfigPath))
		if strings.Contains(config, `"files"`) || !strings.Contains(config, `"mini"`) {
			t.Errorf("agent config = %s, want files replaced by mini", config)
		}
		if got := out.Report.Connected; len(got) != 1 || strings.Join(got[0].Removed, ",") != "files" {
			t.Errorf("connected = %+v, want files removed from Claude Code", got)
		}
	})
	t.Run("quitting while the checks run returns only once they stopped", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			configDir := t.TempDir()
			configtest.WriteServer(t, configDir, config.ServerConfig{Name: "files", Command: "files-server"})
			setup := setupFor(configDir)
			setup.AgentsToConnect = []agents.Agent{claudeWithServers(t)}
			var stopped atomic.Bool
			setup.Probe = func(ctx context.Context, _ string, _ config.ServerConfig) error {
				<-ctx.Done()
				time.Sleep(time.Second) // closing the probed server's process
				stopped.Store(true)
				return ctx.Err()
			}
			quitWhileChecking := func(m tea.Model) error {
				m.Update(press("ctrl+c"))
				return nil
			}
			if _, err := Run(Params{Setup: setup, LoadCatalog: noCatalog, Program: quitWhileChecking}); err != nil {
				t.Fatal(err)
			}
			if !stopped.Load() {
				t.Error("Run returned while a check's server was still closing")
			}
		})
	})
	t.Run("an entry whose mini copy failed its check stays", func(t *testing.T) {
		claude, out := run(t, errors.New("connection refused"))
		config := string(testutil.ReadFile(t, claude.ConfigPath))
		if !strings.Contains(config, `"files"`) || !strings.Contains(config, `"mini"`) {
			t.Errorf("agent config = %s, want files kept next to mini", config)
		}
		if got := out.Report.Connected; len(got) != 1 || len(got[0].Kept) != 1 {
			t.Errorf("connected = %+v, want files kept with the check's error", got)
		}
	})
}
