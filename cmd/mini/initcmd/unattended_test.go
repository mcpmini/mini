//go:build test

package initcmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func configuredNames(t *testing.T, configDir string) []string {
	t.Helper()
	servers, err := config.LoadServers(configDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sc := range servers.Loaded {
		names = append(names, sc.Name)
	}
	slices.Sort(names)
	return names
}

func TestRunUnattended(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(
		t,
		f.configDir,
		config.ServerConfig{Name: "linear", Transport: "http", URL: "https://linear.example.com/mcp"},
	)
	claude := f.write(t, "Claude Code", `{"mcpServers":{
		"github":{"type":"http","url":"https://gh.example.com/mcp","headers":{"Authorization":"Bearer ${GH}"}},
		"paused":{"command":"paused-server","disabled":true}}}`)
	codex := f.write(t, "Codex", "[mcp_servers.files]\ncommand = \"files-server\"\ncwd = \"/srv\"\n")
	add := []catalog.Entry{
		{Name: "files", URL: "https://files.example.com/mcp"},
		{Name: "github", URL: "https://gh.example.com/mcp"},
		{Name: "linear", URL: "https://linear.example.com/mcp"},
		{Name: "notion", URL: "https://notion.example.com/mcp", Auth: catalog.AuthOAuth2},
	}

	report := RunUnattended(
		Setup{ConfigDir: f.configDir, Import: []agents.Agent{claude, codex}, Add: add, SelfPath: testSelf},
	)

	if got := configuredNames(
		t,
		f.configDir,
	); !reflect.DeepEqual(
		got,
		[]string{"files", "github", "linear", "notion"},
	) {
		t.Errorf("configured = %v, want files and github imported, notion added, and no switched-off server", got)
	}
	if !reflect.DeepEqual(report.AlreadyConfigured, []string{"linear"}) {
		t.Errorf("already configured = %v, want linear", report.AlreadyConfigured)
	}
	if !reflect.DeepEqual(report.AddCoveredByImport, []string{"files", "github"}) {
		t.Errorf("from import = %v, want files and github, which the imports cover", report.AddCoveredByImport)
	}
	wantSkipped := []SkippedServer{{Agent: "Claude Code", Name: "paused", Reason: SkipSwitchedOff}}
	if !reflect.DeepEqual(report.Import.Skipped, wantSkipped) ||
		!reflect.DeepEqual(report.Import.DroppedSettings, map[string][]string{"files": {"cwd"}}) ||
		report.Failed() {
		t.Errorf(
			"skipped = %+v, ignored = %v, failed = %v; want paused named as switched off, files' cwd named, no failure",
			report.Import.Skipped,
			report.Import.DroppedSettings,
			report.Failed(),
		)
	}
	github, err := config.ReadUnexpandedServer(f.configDir, "github")
	if err != nil || github.Headers["Authorization"] != "Bearer ${GH}" {
		t.Errorf("github = %+v, %v; want the imported copy, which carries the header", github, err)
	}
}

func TestRunUnattended_namesASecondConfigItLeavesOut(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(t, "Claude Code", `{"mcpServers":{"github":{"command":"gh-server-a"}}}`)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"github":{"command":"gh-server-b"}}}`)

	report := RunUnattended(
		Setup{ConfigDir: f.configDir, Import: []agents.Agent{claude, cursor}, SelfPath: testSelf},
	)

	want := []SkippedServer{{Agent: "Cursor", Name: "github", Reason: SkipSecondConfig}}
	if !reflect.DeepEqual(report.Import.Skipped, want) ||
		!reflect.DeepEqual(configuredNames(t, f.configDir), []string{"github"}) {
		t.Errorf(
			"skipped = %+v, configured = %v; want Claude Code's github imported and Cursor's named",
			report.Import.Skipped,
			configuredNames(t, f.configDir),
		)
	}
}

func TestRunUnattended_anEnabledConfigWinsANameFromASwitchedOffOne(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(t, "Claude Code", `{"mcpServers":{"github":{"command":"gh-server-a","disabled":true}}}`)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"github":{"command":"gh-server-b"}}}`)

	report := RunUnattended(
		Setup{ConfigDir: f.configDir, Import: []agents.Agent{claude, cursor}, SelfPath: testSelf},
	)

	github, err := config.ReadUnexpandedServer(f.configDir, "github")
	if err != nil || github.Command != "gh-server-b" {
		t.Errorf("github = %+v, %v; want Cursor's enabled config imported", github, err)
	}
	want := []SkippedServer{{Agent: "Claude Code", Name: "github", Reason: SkipSwitchedOff}}
	if !reflect.DeepEqual(report.Import.Skipped, want) {
		t.Errorf("skipped = %+v, want Claude Code's switched-off github named", report.Import.Skipped)
	}
}

func TestReport_failed(t *testing.T) {
	for name, tt := range map[string]struct {
		report Report
		want   bool
	}{
		"nothing failed":        {Report{}, false},
		"a server write failed": {Report{WriteErrors: []ServerError{{Name: "x", Err: errors.New("disk full")}}}, true},
		"servers can't be read": {Report{ReadServersErr: errors.New("permission denied")}, true},
		"an agent config can't be read": {
			Report{Import: ImportPlan{Unreadable: []UnreadableAgent{{Agent: "Cursor", Err: errors.New("invalid character")}}}},
			true,
		},
	} {
		if got := tt.report.Failed(); got != tt.want {
			t.Errorf("%s: Failed() = %v, want %v", name, got, tt.want)
		}
	}
}

func TestRunUnattended_aServerThatFailsToWriteLosesItsNotes(t *testing.T) {
	f := newApplyFixture(t)
	codex := f.write(t, "Codex", "[mcp_servers.files]\ncommand = \"files-server\"\ncwd = \"/srv\"\n")
	servers := filepath.Join(f.configDir, "servers")
	if err := os.Mkdir(servers, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(
		func() { _ = os.Chmod(servers, 0o700) },
	) //nolint:errcheck // TempDir cleanup reports a dir it can't remove

	report := RunUnattended(Setup{ConfigDir: f.configDir, Import: []agents.Agent{codex}, SelfPath: testSelf})

	if len(report.WriteErrors) != 1 || report.WriteErrors[0].Name != "files" ||
		len(report.Import.DroppedSettings) != 0 {
		t.Errorf(
			"write errors = %+v, ignored = %v; want files failed and its dropped cwd not noted",
			report.WriteErrors,
			report.Import.DroppedSettings,
		)
	}
}

func TestSetupWrite_writesWhatThePlanHasPickedAndReportsOnlyThat(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(t, "Claude Code", `{"mcpServers":{"files":{"command":"files-server"}}}`)
	codex := f.write(t, "Codex", "[mcp_servers.files]\ncommand = \"other-files\"\ncwd = \"/srv\"\n")
	setup := Setup{ConfigDir: f.configDir, Import: []agents.Agent{claude, codex}, SelfPath: testSelf}
	plan, err := setup.Plan()
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Import.Candidates {
		plan.Import.Candidates[i].Picked = plan.Import.Candidates[i].Server.Name == "files-2"
	}

	report := setup.Write(plan)

	if got := configuredNames(t, f.configDir); !reflect.DeepEqual(got, []string{"files-2"}) {
		t.Errorf("configured = %v, want only the picked files-2", got)
	}
	if len(report.Import.Skipped) != 0 ||
		!reflect.DeepEqual(report.Import.DroppedSettings, map[string][]string{"files-2": {"cwd"}}) {
		t.Errorf("skipped = %+v, dropped settings = %v; want Codex's files not listed as left behind, "+
			"and notes only for files-2", report.Import.Skipped, report.Import.DroppedSettings)
	}
}

func TestSetupWrite_noSecondConfigLineWhenTheFirstWasUnpickedToo(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(t, "Claude Code", `{"mcpServers":{"github":{"command":"gh-server-a"}}}`)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"github":{"command":"gh-server-b"}}}`)
	setup := Setup{ConfigDir: f.configDir, Import: []agents.Agent{claude, cursor}, SelfPath: testSelf}
	plan, err := setup.Plan()
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Import.Candidates {
		plan.Import.Candidates[i].Picked = false
	}

	report := setup.Write(plan)

	if len(report.Import.Skipped) != 0 {
		t.Errorf("skipped = %+v; want no line saying another github is imported instead", report.Import.Skipped)
	}
}

func TestRunUnattended_aCatalogAddIsntCreditedWithASwitchedOffConfigOfTheSameName(t *testing.T) {
	f := newApplyFixture(t)
	codex := f.write(t, "Codex", "[mcp_servers.notion]\ncommand = \"notion-server\"\ncwd = \"/srv\"\nenabled = false\n")

	report := RunUnattended(Setup{
		ConfigDir: f.configDir,
		Import:    []agents.Agent{codex},
		Add:       []catalog.Entry{{Name: "notion", URL: "https://notion.example.com/mcp"}},
		SelfPath:  testSelf,
	})

	want := []SkippedServer{{Agent: "Codex", Name: "notion", Reason: SkipSwitchedOff}}
	if !reflect.DeepEqual(report.Import.Skipped, want) || len(report.Import.DroppedSettings) != 0 {
		t.Errorf("skipped = %+v, dropped settings = %v; want Codex's notion named as switched off "+
			"and no cwd note for the catalog's notion", report.Import.Skipped, report.Import.DroppedSettings)
	}
}
