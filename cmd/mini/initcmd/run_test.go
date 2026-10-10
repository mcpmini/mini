//go:build test

package initcmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

func TestRun(t *testing.T) {
	t.Run("closing stops the OAuth checks and leaves mini as it was", func(t *testing.T) {
		probe := newFakeProbe(true)
		setup := Setup{
			ConfigDir: t.TempDir(),
			Add:       []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}},
			Probe:     probe.probe,
		}
		run := mustStart(t, setup, Plan{Add: setup.Add})
		run.Save()
		waitStarted(t, probe)

		run.Close()

		if _, finished := probe.counts(); finished != 1 {
			t.Errorf("checks finished = %d, want 1: a check still running could write after Close", finished)
		}
		if config.ServerFileExists(setup.ConfigDir, "notes") {
			t.Error("notes is in mini after Close, want nothing saved until Finish")
		}
		assertNoStage(t, run)
	})

	t.Run("finishing commits the saved servers to mini", func(t *testing.T) {
		setup := Setup{
			ConfigDir: t.TempDir(),
			Add:       []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}},
			Probe:     newFakeProbe(false).probe,
		}
		run := mustStart(t, setup, Plan{Add: setup.Add})
		run.Save()
		if config.ServerFileExists(setup.ConfigDir, "notes") {
			t.Fatal("notes is in mini after Save, want it staged until Finish")
		}

		report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})

		if !config.ServerFileExists(setup.ConfigDir, "notes") || len(report.WriteErrors) > 0 {
			t.Errorf("notes in mini = %v, write errors %v; want notes committed",
				config.ServerFileExists(setup.ConfigDir, "notes"), report.WriteErrors)
		}
		assertNoStage(t, run)
	})

	t.Run("finishing reports the agents as the connect left them", func(t *testing.T) {
		f := newApplyFixture(t)
		cursor := f.write(t, "Cursor", `{"mcpServers":{}}`)
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		setup := Setup{ConfigDir: f.configDir, AgentsToConnect: []agents.Agent{cursor}, SelfPath: self}
		run := mustStart(t, setup, Plan{})
		run.Save()

		report := run.Finish(
			context.Background(),
			ConnectParams{Agents: []agents.Agent{cursor}, Choice: ConnectOnly},
		)

		if !slices.ContainsFunc(report.Agents.MiniServes, func(a agents.Agent) bool { return a.Name == "Cursor" }) {
			t.Errorf(
				"agents = %+v, want Cursor serving mini: the report must read Cursor after the connect",
				report.Agents,
			)
		}
	})
}

func mustStart(t *testing.T, setup Setup, p Plan) *Run {
	t.Helper()
	run, err := setup.Start(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	return run
}

func assertNoStage(t *testing.T, run *Run) {
	t.Helper()
	if _, err := os.Stat(run.StageDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stage %s: %v, want it removed", run.StageDir(), err)
	}
}
