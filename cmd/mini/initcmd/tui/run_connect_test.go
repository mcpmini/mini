package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

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
		// Catalog comes first and is left from Continue, which saves; then Connect is shown.
		c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("linear")}}
		out, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(c), Program: pressing(keys...)})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	t.Run("just connect adds mini to the agent and reports it", func(t *testing.T) {
		claude := claudeWithServers(t)
		out := run(t, claude, "tab", "enter", "enter")
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
			pressing("tab", "enter")(m)
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
		"don't connect leaves the agent as it was":            {"tab", "enter", "down", "enter"},
		"quitting on Connect after the save leaves the agent": {"tab", "enter", "ctrl+c"},
		"a ctrl+c right after choosing leaves the agent":      {"tab", "enter", "enter", "ctrl+c"},
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
		m.View()
		for _, key := range keys {
			cmd := pressKey(m, key)
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

func TestRun_goingBackToUntickAnImportLowersWhatConnectRemoves(t *testing.T) {
	claude := agents.Known(t.TempDir())[0]
	testutil.WriteFile(
		t,
		claude.ConfigPath,
		`{"mcpServers":{"files":{"command":"files-server"},"notes":{"command":"notes-server"}}}`,
	)
	setup := setupFor(t.TempDir(), claude)
	setup.AgentsToConnect = []agents.Agent{claude}
	setup.Probe = func(context.Context, string, config.ServerConfig) error { return nil }
	var connect string
	program := func(m tea.Model) error {
		deliver(m, m.Init())
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m.View()
		// Import both and reach Connect; back, untick files, return to Connect.
		for _, key := range []string{"tab", "enter", "esc", "up", "space", "tab", "enter"} {
			cmd := pressKey(m, key)
			deliver(m, cmd)
		}
		connect = shown(m.(*app))
		_, cmd := m.Update(press("enter"))
		deliver(m, cmd)
		return nil
	}

	if _, err := Run(Params{Setup: setup, LoadCatalog: noCatalog, Program: program}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(connect, "1 MCP will be added to mini: notes") {
		t.Errorf("Connect:\n%s\nwant notes named as added: files was unticked on Import", connect)
	}
	if !strings.Contains(connect, "removing notes") || strings.Contains(connect, "removing files") {
		t.Errorf("Connect:\n%s\nwant only notes removed: files was unticked on Import", connect)
	}
	config := string(testutil.ReadFile(t, claude.ConfigPath))
	if !strings.Contains(config, `"files"`) || strings.Contains(config, `"notes"`) {
		t.Errorf("agent config = %s, want notes removed and files kept: mini doesn't run files", config)
	}
}
