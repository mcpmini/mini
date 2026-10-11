//go:build test

package initcmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
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

	t.Run("a login is saved in the stage for a server the run adds, and in mini for the rest", func(t *testing.T) {
		setup := Setup{
			ConfigDir: t.TempDir(),
			Add:       []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}},
			Probe:     newFakeProbe(false).probe,
		}
		configtest.WriteServer(t, setup.ConfigDir, config.ServerConfig{Name: "files", URL: "https://files.example/mcp"})
		run := mustStart(t, setup, Plan{Add: setup.Add})
		run.Save()

		if got := run.ConfigDirFor("files"); got != setup.ConfigDir {
			t.Errorf(
				"files' login goes to %q, want mini's config dir: quitting mustn't undo a login to a server mini has",
				got,
			)
		}
		if got := run.ConfigDirFor("notes"); got != run.stage.dir {
			t.Errorf("notes' login goes to %q, want the stage, which holds notes until Finish", got)
		}
	})

	t.Run("a server added outside init while it ran is reported, not imported", func(t *testing.T) {
		setup := Setup{
			ConfigDir: t.TempDir(),
			Add:       []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}},
			Probe:     newFakeProbe(false).probe,
		}
		run := mustStart(t, setup, Plan{Add: setup.Add})
		run.Save()
		configtest.WriteServer(
			t,
			setup.ConfigDir,
			config.ServerConfig{Name: "notes", URL: "https://theirs.example/mcp"},
		)

		report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})

		if len(report.WriteErrors) != 1 || !errors.Is(report.WriteErrors[0].Err, errAddedOutsideInit) {
			t.Errorf("write errors = %v, want notes reported as added outside init", report.WriteErrors)
		}
	})

	t.Run("a stage that can't be created reports every server as failed", func(t *testing.T) {
		configDir := filepath.Join(t.TempDir(), "config")
		testutil.WriteFile(t, configDir, "a file where the config dir should be")
		setup := Setup{ConfigDir: configDir, Add: []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}}}
		run := mustStart(t, setup, Plan{Add: setup.Add})
		run.Save()

		report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})

		if len(report.WriteErrors) != 1 || report.WriteErrors[0].Name != "notes" {
			t.Errorf("write errors = %v, want notes failed", report.WriteErrors)
		}
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
	run := setup.Start(p)
	t.Cleanup(run.Close)
	return run
}

func assertNoStage(t *testing.T, run *Run) {
	t.Helper()
	if _, err := os.Stat(run.stage.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stage %s: %v, want it removed", run.stage.dir, err)
	}
}

func TestRunPlanConnect_checksEachServerWhereItsLoginIsSaved(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "files-server"})
	entries := `"files":{"command":"files-server"},"notes":{"type":"http","url":"https://notes.example/mcp"}`
	claude := f.write(t, "Claude Code", `{"mcpServers":{`+entries+`}}`)
	var mu sync.Mutex
	probedIn := map[string]string{}
	setup := Setup{
		ConfigDir:       f.configDir,
		Add:             []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}},
		AgentsToConnect: []agents.Agent{claude},
		Probe: func(_ context.Context, configDir string, sc config.ServerConfig) error {
			mu.Lock()
			defer mu.Unlock()
			probedIn[sc.Name] = configDir
			return nil
		},
	}
	run := mustStart(t, setup, Plan{Add: setup.Add})
	run.Save()
	plan, err := run.PlanConnect()
	if err != nil {
		t.Fatal(err)
	}

	plan.Check(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if probedIn["files"] != f.configDir {
		t.Errorf("files checked in %q, want mini's config dir: a token refreshed by the check must outlive a quit",
			probedIn["files"])
	}
	if probedIn["notes"] != run.stage.dir {
		t.Errorf("notes checked in %q, want the stage, which holds it until Finish", probedIn["notes"])
	}
}
