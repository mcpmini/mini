//go:build test

package initcmd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestRun(t *testing.T) {
	t.Run("finishing reports the servers it committed", func(t *testing.T) {
		setup := Setup{
			ConfigDir: t.TempDir(),
			Add:       []catalog.Entry{{Name: "notes", URL: "https://notes.example/mcp"}},
			Probe:     newFakeProbe(false).probe,
		}
		configtest.WriteServer(t, setup.ConfigDir, config.ServerConfig{Name: "files", URL: "https://files.example/mcp"})
		run := mustStart(t, setup, Plan{Add: setup.Add})
		run.Save()
		report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})
		if !slices.Equal(report.Added, []string{"notes"}) {
			t.Errorf("Added = %v, want only notes: files was in mini before the run", report.Added)
		}
	})

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
		if configtest.ServerSaved(t, setup.ConfigDir, "notes") {
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
		if configtest.ServerSaved(t, setup.ConfigDir, "notes") {
			t.Fatal("notes is in mini after Save, want it staged until Finish")
		}

		report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})

		if !configtest.ServerSaved(t, setup.ConfigDir, "notes") || len(report.WriteErrors) > 0 {
			t.Errorf("notes in mini = %v, write errors %v; want notes committed",
				configtest.ServerSaved(t, setup.ConfigDir, "notes"), report.WriteErrors)
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

func TestRunFinish_keepsTheAgentsEntryWhenItsServerFailsToCommit(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(t, "Claude Code", `{"mcpServers":{"files":{"command":"files-server"}}}`)
	setup := Setup{ConfigDir: f.configDir, Import: []agents.Agent{claude}, SelfPath: f.mini}
	plan, err := setup.Plan()
	if err != nil {
		t.Fatal(err)
	}
	run := mustStart(t, setup, plan)
	run.Save()
	// Someone else adds a different files after the save, so init's can't commit.
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "files", Command: "other-files-server"})

	report := run.Finish(context.Background(), ConnectParams{
		Agents: []agents.Agent{claude},
		Choice: ConnectAndRemove,
		Removals: Removals{
			Checks:  map[string]error{"files": nil},
			ByAgent: map[string][]string{"Claude Code": {"files"}},
		},
	})

	if len(report.WriteErrors) != 1 || !slices.Contains(entryNamesIn(t, claude), "files") {
		t.Errorf(
			"write errors = %+v, Claude Code's entries = %v; want files kept: mini doesn't run the agent's files",
			report.WriteErrors,
			entryNamesIn(t, claude),
		)
	}
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

func TestRun_catalogAuthGuidesStagedAndCommittedServers(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind catalog.AuthKind
		meta auth.ServerMeta
		step string
	}{
		{name: "token", kind: catalog.AuthToken, step: "requires manual configuration, edit service.yaml"},
		{name: "app", kind: catalog.AuthOAuth2App, step: "requires a custom OAuth app, edit service.yaml"},
		{
			name: "token with DCR", kind: catalog.AuthToken, meta: auth.ServerMeta{RegistrationURL: "https://auth.example/register"},
			step: "requires a login, run: mini auth service",
		},
		{
			name: "token with CIMD", kind: catalog.AuthToken, meta: auth.ServerMeta{CIMDSupported: true},
			step: "requires a login, run: mini auth service",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setup := Setup{
				ConfigDir: t.TempDir(),
				Probe:     answersWithOAuth,
				Add: []catalog.Entry{
					{
						Name:     "service",
						URL:      "https://service.example/mcp",
						Auth:     tt.kind,
						SetupURL: "https://service.example/setup",
					},
				},
				Discover: func(context.Context, string) (*auth.ServerMeta, error) { return &tt.meta, nil },
			}
			run := mustStart(t, setup, Plan{Add: setup.Add})
			run.Save()
			run.session.WaitChecks()
			statuses, err := run.ServerStatuses()
			if err != nil {
				t.Fatal(err)
			}
			if got := statusNamed(t, statuses, "service").Step("service.yaml", "mini auth service"); got != tt.step {
				t.Errorf("staged step = %q, want %q", got, tt.step)
			}
			report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})
			if report.Failed() {
				t.Fatalf("report failed: %+v", report)
			}
			if got := statusNamed(
				t,
				report.Servers,
				"service",
			).Step("service.yaml", "mini auth service"); got != tt.step {
				t.Errorf("committed step = %q, want %q", got, tt.step)
			}
			wantSummary := strings.ReplaceAll(
				strings.TrimSuffix(tt.step, "mini auth service"),
				"service.yaml",
				config.ServerPath(setup.ConfigDir, "service"),
			)
			requireLines(t, plainSummary(report), wantSummary)
		})
	}
}

func newCatalogTokenRun(t *testing.T) *Run {
	t.Helper()
	setup := Setup{
		ConfigDir: t.TempDir(),
		Probe:     answersWithOAuth,
		Discover:  registersOnlyAt(),
		Add: []catalog.Entry{
			{
				Name:     "github",
				URL:      "https://github.example/mcp",
				Auth:     catalog.AuthToken,
				SetupURL: "https://github.example/tokens",
			},
		},
	}
	run := mustStart(t, setup, Plan{Add: setup.Add})
	run.Save()
	run.session.WaitChecks()
	return run
}

func TestRun_replacingACatalogPickWithAnImportDropsItsTokenAdvice(t *testing.T) {
	run := newCatalogTokenRun(t)
	if got := statusNamed(t, stagedStatuses(t, run), "github").Readiness; !reflect.DeepEqual(got, NeedsTokenState{}) {
		t.Fatalf("catalog readiness = %T, want manual configuration", got)
	}
	run.Plan.Import.Candidates = []Candidate{{Server: httpServer("github", "https://github.example/mcp"), Picked: true}}
	run.Save()
	run.session.WaitChecks()
	if got := statusNamed(t, stagedStatuses(t, run), "github").Readiness; !reflect.DeepEqual(got, NeedsOwnAppState{}) {
		t.Errorf(
			"imported readiness = %T, want custom app even though catalog selection has the same name and URL",
			got,
		)
	}
	report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})
	if report.Failed() {
		t.Fatalf("report failed: %+v", report)
	}
	if got := statusNamed(t, report.Servers, "github").Readiness; !reflect.DeepEqual(got, NeedsOwnAppState{}) {
		t.Errorf("committed import readiness = %T, want custom app", got)
	}
}

func stagedStatuses(t *testing.T, run *Run) []ServerStatus {
	t.Helper()
	statuses, err := run.ServerStatuses()
	if err != nil {
		t.Fatal(err)
	}
	return statuses
}

func TestRun_aFailedReplacementKeepsTheSavedCatalogAdvice(t *testing.T) {
	run := newCatalogTokenRun(t)
	invalid := httpServer("github", "https://replacement.example/mcp")
	invalid.HandshakeTimeout = "nonsense"
	run.Plan.Import.Candidates = []Candidate{{Server: invalid, Picked: true}}
	run.Save()
	run.session.WaitChecks()
	if failures := run.SaveFailures(); len(failures) != 1 {
		t.Fatalf("save failures = %v, want rejected replacement", failures)
	}
	if got := statusNamed(t, stagedStatuses(t, run), "github").Readiness; !reflect.DeepEqual(got, NeedsTokenState{}) {
		t.Errorf("readiness after rejected replacement = %T, want the saved catalog's token advice", got)
	}
	report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})
	if got := statusNamed(t, report.Servers, "github").Readiness; !reflect.DeepEqual(got, NeedsTokenState{}) {
		t.Errorf("committed readiness = %T, want the saved catalog's token advice", got)
	}
}

func TestRun_aCommitConflictDoesNotGiveAnUnrelatedServerCatalogAdvice(t *testing.T) {
	run := newCatalogTokenRun(t)
	configtest.WriteServer(t, run.setup.ConfigDir, oauthServer("github", ""))
	report := run.Finish(context.Background(), ConnectParams{Choice: DontConnect})
	if len(report.WriteErrors) != 1 || !errors.Is(report.WriteErrors[0].Err, errAddedOutsideInit) {
		t.Fatalf("write errors = %v, want commit conflict", report.WriteErrors)
	}
	if got := statusNamed(t, report.Servers, "github").Readiness; !reflect.DeepEqual(got, NeedsOwnAppState{}) {
		t.Errorf("unrelated server readiness = %T, want custom app without catalog advice", got)
	}
}
