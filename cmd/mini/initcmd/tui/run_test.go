package tui

import (
	"os"
	"path/filepath"
	"slices"
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

func setupFor(configDir string, list ...agents.Agent) initcmd.Setup {
	return initcmd.Setup{ConfigDir: configDir, Import: list}
}

func pickedNames(plan initcmd.Plan) []string {
	var names []string
	for _, c := range plan.Import.Candidates {
		if c.Picked {
			names = append(names, c.Server.Name)
		}
	}
	return names
}

func TestRun_quittingReturnsNoPlanAndTouchesNothing(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	plan, quit, err := Run(
		Params{Setup: setupFor(configDir, stdioAgent("Codex", "files")), Program: pressing("ctrl+c")},
	)
	if err != nil || !quit || len(plan.Import.Candidates) != 0 {
		t.Fatalf("Run = %+v, %v, %v; want a quit with no plan", plan, quit, err)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Errorf("config dir after quitting: %v, want it untouched", err)
	}
}

func TestRun_finishingReturnsThePlanWithTheTicks(t *testing.T) {
	plan, quit, err := Run(Params{
		Setup:   setupFor(t.TempDir(), stdioAgent("Codex", "files", "notes")),
		Program: pressing("space", "enter"),
	})
	if err != nil || quit {
		t.Fatalf("Run = %v, %v; want it finished", quit, err)
	}
	if got := pickedNames(plan); !slices.Equal(got, []string{"notes"}) {
		t.Errorf("picked = %v, want only notes: files was unticked", got)
	}
}

func TestRun_withNothingToImportShowsNoUI(t *testing.T) {
	shown := false
	program := func(tea.Model) error {
		shown = true
		return nil
	}
	_, quit, err := Run(Params{Setup: setupFor(t.TempDir(), stdioAgent("Codex")), Program: program})
	if err != nil || quit || shown {
		t.Errorf("Run: quit = %v, err = %v, UI shown = %v; want a finished run with no UI", quit, err, shown)
	}
}
