//go:build integration && !windows

package tui

import (
	"os"
	"path/filepath"
	"slices"
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

func TestIntegrationInitUI_writesTheTickedImportsAndCatalogServersAndPrintsTheSummary(t *testing.T) {
	configDir := t.TempDir()
	term := startTerminal(
		t,
		terminalParams{home: homeWithClaudeServers(t), configDir: configDir, args: []string{"init"}},
	)

	term.waitFor("Import servers from Claude Code")
	term.waitFor("[x] files")
	term.press("space", "enter")
	term.waitFor("Add servers from the catalog")
	term.press("/", "s", "e", "n", "t", "r", "y", "enter", "space", "enter")
	term.waitFor("mini is set up with 2 servers")

	if code := term.exitCode(); code != 0 {
		t.Errorf("exit code = %d, want 0; screen:\n%s", code, term.text())
	}
	if files := serverFiles(t, configDir); !slices.Equal(files, []string{"notes.yaml", "sentry.yaml"}) {
		t.Errorf("server files = %v, want notes and the catalog's sentry: files was unticked", files)
	}
	if term.altScreen() {
		t.Error("the terminal is still on the alternate screen after init")
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
