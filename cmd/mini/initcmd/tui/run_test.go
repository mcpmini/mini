package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
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

func TestRun_goingBackToTickAnImportDropsTheSameCatalogServer(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{{Name: "sentry", URL: "https://mcp.sentry.example/mcp"}}}
	plan, quit, err := Run(Params{
		Setup:   setupFor(t.TempDir(), stdioAgent("Codex", "sentry")),
		Catalog: c,
		// Untick the import, tick the catalog's sentry, go back, tick the import again, finish.
		Program: pressing("space", "enter", "space", "esc", "space", "enter", "enter"),
	})
	if err != nil || quit {
		t.Fatalf("Run = %v, %v; want it finished", quit, err)
	}
	if got := pickedNames(plan); !slices.Equal(got, []string{"sentry"}) || len(plan.Add) != 0 {
		t.Errorf("imports = %v, adds = %v; want only the imported sentry", got, plan.Add)
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
	c := catalog.Catalog{Entries: []catalog.Entry{
		{Name: "linear", URL: "https://mcp.linear.example/mcp"},
		{Name: "sentry", URL: "https://mcp.sentry.example/mcp"},
	}}
	plan, _, err := Run(Params{Setup: setupFor(configDir), Catalog: c, Program: pressing("space", "enter")})
	if err != nil || len(plan.Add) != 1 || plan.Add[0].Name != "sentry" {
		t.Errorf("adds = %v, err = %v; want sentry: linear is configured, so the first row is sentry", plan.Add, err)
	}
}
