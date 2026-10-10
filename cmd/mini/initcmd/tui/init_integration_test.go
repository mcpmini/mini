//go:build integration && !windows

package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func homeWithClaudeServers(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	testutil.WriteFile(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"files":{"command":"files-server"},"notes":{"command":"notes-server"}}}`)
	return home
}

func TestIntegrationInitUI_writesTheTickedServersConnectsTheAgentAndPrintsTheSummary(t *testing.T) {
	configDir := t.TempDir()
	home := homeWithClaudeServers(t)
	term := startTerminal(t, terminalParams{home: home, configDir: configDir, args: []string{"init"}})

	term.waitFor("Import servers from Claude Code")
	term.waitFor("[x] files")
	term.press("space", "tab", "enter")
	term.waitFor("Add servers from the catalog")
	term.waitFor("[ ] Sentry")
	term.press("/", "s", "e", "n", "t", "r", "y", "enter", "space", "tab", "enter")
	term.waitFor("sentry  needs a login")
	term.press("down", "enter")
	term.waitFor("Connect mini to Claude Code")
	term.press("down")
	term.waitFor("> Just connect mini")
	term.press("enter")
	term.waitFor("mini is set up with 2 servers")
	term.waitFor("Restart Claude Code to start using mini.")

	if code := term.exitCode(); code != 0 {
		t.Errorf("exit code = %d, want 0; screen:\n%s", code, term.text())
	}
	if files := serverFiles(t, configDir); !slices.Equal(files, []string{"notes.yaml", "sentry.yaml"}) {
		t.Errorf("server files = %v, want notes and the catalog's sentry: files was unticked", files)
	}
	if term.altScreen() {
		t.Error("the terminal is still on the alternate screen after init")
	}
	agentConfig := string(testutil.ReadFile(t, filepath.Join(home, ".claude.json")))
	if !strings.Contains(agentConfig, `"mini"`) || !strings.Contains(agentConfig, `"files"`) {
		t.Errorf(".claude.json = %s\nwant a mini entry added next to the existing servers", agentConfig)
	}
	backup := string(testutil.ReadFile(t, filepath.Join(home, ".claude.minibackup.json")))
	if strings.Contains(backup, `"mini"`) {
		t.Errorf("backup = %s, want the config as it was before init", backup)
	}
}

func TestIntegrationInitUI_ctrlCLeavesNoServersAndRestoresTheTerminal(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	term := startTerminal(
		t,
		terminalParams{home: homeWithClaudeServers(t), configDir: configDir, args: []string{"init"}},
	)

	term.waitFor("[x] files")
	term.press("ctrl+c")
	term.waitFor("nothing was written")

	if code := term.exitCode(); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("config dir after ctrl+c: %v, want it never created", err)
	}
	if term.altScreen() {
		t.Error("the terminal is still on the alternate screen after ctrl+c")
	}
}

func TestIntegrationInitUI_ctrlCAfterCatalogKeepsTheSavedServers(t *testing.T) {
	configDir := t.TempDir()
	term := startTerminal(
		t,
		terminalParams{home: homeWithClaudeServers(t), configDir: configDir, args: []string{"init"}},
	)

	term.waitFor("[x] files")
	term.press("tab", "enter")
	term.waitFor("Add servers from the catalog")
	term.press("/", "s", "e", "n", "t", "r", "y", "enter", "space", "tab", "enter")
	term.waitFor("Finish setting up these servers")
	term.press("ctrl+c")
	term.waitFor("the servers above were saved")

	if code := term.exitCode(); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if files := serverFiles(t, configDir); !slices.Equal(files, []string{"files.yaml", "notes.yaml", "sentry.yaml"}) {
		t.Errorf("server files = %v, want everything saved on leaving Catalog", files)
	}
}

func serverFiles(t *testing.T, configDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(configDir, "servers"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
