package tui

import (
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
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

func pressing(keys ...string) func(tea.Model) error {
	return func(m tea.Model) error {
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		for _, key := range keys {
			m.Update(press(key))
		}
		return nil
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

func setupFor(configDir string, list ...agents.Agent) initcmd.Setup {
	return initcmd.Setup{ConfigDir: configDir, Import: list}
}

func TestRun_quittingWritesNothing(t *testing.T) {
	configDir := t.TempDir()
	out, err := Run(Params{Setup: setupFor(configDir, stdioAgent("Codex", "files")), Program: pressing("ctrl+c")})
	if err != nil || !out.Quit {
		t.Fatalf("Run = %+v, %v; want a quit", out, err)
	}
	if files := serverFiles(t, configDir); len(files) != 0 {
		t.Errorf("server files after quitting: %v, want none", files)
	}
}

func TestRun_finishingWritesTheTickedServers(t *testing.T) {
	configDir := t.TempDir()
	out, err := Run(Params{
		Setup:   setupFor(configDir, stdioAgent("Codex", "files", "notes")),
		Program: pressing("space", "enter"),
	})
	if err != nil || out.Quit {
		t.Fatalf("Run = %+v, %v; want it finished", out, err)
	}
	if files := serverFiles(t, configDir); !slices.Equal(files, []string{"notes.yaml"}) {
		t.Errorf("server files = %v, want only notes: files was unticked", files)
	}
}

func TestRun_withNothingToImportShowsNoUI(t *testing.T) {
	configDir := t.TempDir()
	shown := false
	program := func(tea.Model) error {
		shown = true
		return nil
	}
	out, err := Run(Params{Setup: setupFor(configDir, stdioAgent("Codex")), Program: program})
	if err != nil || out.Quit || shown {
		t.Errorf("Run = %+v, %v, UI shown = %v; want a finished run with no UI", out, err, shown)
	}
}

func TestRun_logOutputWhileTheUIRunsGoesToAFile(t *testing.T) {
	configDir := t.TempDir()
	program := func(m tea.Model) error {
		log.Print("probe failed")
		return pressing("enter")(m)
	}
	out, err := Run(Params{Setup: setupFor(configDir, stdioAgent("Codex", "files")), Program: program})
	want := filepath.Join(configDir, "internal", "init.log")
	if err != nil || out.LogFile != want {
		t.Fatalf("Run = %+v, %v; want the log file %s named", out, err, want)
	}
	data, err := os.ReadFile(want) //fileiolint:allow read the log file the run wrote
	if err != nil || !strings.HasSuffix(string(data), "probe failed\n") {
		t.Errorf("log file = %q, %v; want the logged line", data, err)
	}
	if log.Writer() != os.Stderr {
		t.Errorf("log output wasn't restored after the UI")
	}
}
