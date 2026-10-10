package initcmd

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestConnectPlan_removesOnlyWhatMiniWillServeAndWhatPassedItsCheck(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "notes", Command: "notes-server"})
	entries := `"files":{"command":"files-server"},"notes":{"command":"notes-server"}`
	claude := f.write(t, "Claude Code", `{"mcpServers":{`+entries+`}}`)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"mini":{"command":"mini","args":["connect"]},`+entries+`}}`)
	setup := Setup{
		ConfigDir:       f.configDir,
		AgentsToConnect: []agents.Agent{claude, cursor},
		Probe: func(_ context.Context, _ string, sc config.ServerConfig) error {
			if sc.Name == "notes" {
				return errors.New("connection refused")
			}
			return nil
		},
	}
	plan, err := setup.planConnect(f.configDir)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasDuplicates("Claude Code") || plan.HasDuplicates("Cursor") {
		t.Errorf("duplicates: Claude Code %v, Cursor %v; want only Claude Code's: Cursor's mini entry "+
			"may not serve these servers, so nothing of it is removed",
			plan.HasDuplicates("Claude Code"), plan.HasDuplicates("Cursor"))
	}
	removals := plan.Check(context.Background())
	if got := removals.ByAgent["Claude Code"]; !slices.Equal(got, []string{"files"}) {
		t.Errorf("Claude Code would lose %v, want only files: notes failed its check", got)
	}
	if removals.Checks["notes"] == nil || removals.Checks["files"] != nil {
		t.Errorf("checks = %v, want files passing and notes failing", removals.Checks)
	}
}

func TestSetupConnect_reportsAnEntryThatChangedSinceConnectCountedIt(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	claude := f.write(t, "Claude Code", `{"mcpServers":{"files":{"command":"files-server-v2"}}}`)
	results := Setup{ConfigDir: f.configDir}.connectAgents(context.Background(), ConnectParams{
		Agents: []agents.Agent{claude},
		Choice: ConnectAndRemove,
		Removals: Removals{
			Checks:  map[string]error{"files": nil},
			ByAgent: map[string][]string{"Claude Code": {"files"}},
		},
	})
	if len(results) != 1 || !slices.Equal(results[0].Changed, []string{"files"}) || len(results[0].Removed) > 0 {
		t.Errorf("results = %+v, want files kept and reported as changed since Connect counted it", results)
	}
}

func TestSetupConnect_anEntryDeletedSinceConnectCountedItIsNotReported(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	claude := f.write(t, "Claude Code", `{"mcpServers":{}}`)
	results := Setup{ConfigDir: f.configDir}.connectAgents(context.Background(), ConnectParams{
		Agents: []agents.Agent{claude},
		Choice: ConnectAndRemove,
		Removals: Removals{
			Checks:  map[string]error{"files": nil},
			ByAgent: map[string][]string{"Claude Code": {"files"}},
		},
	})
	if len(results) != 1 || len(results[0].Changed) > 0 {
		t.Errorf("results = %+v, want nothing reported for files, which the user deleted", results)
	}
}
