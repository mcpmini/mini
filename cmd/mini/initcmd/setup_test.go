//go:build test

package initcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
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
	wantSkipped := []SkippedServer{{Agent: "Claude Code", Name: "paused", Cause: SwitchedOffSkip{}}}
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

	want := []SkippedServer{{Agent: "Cursor", Name: "github", Cause: SecondConfigSkip{}}}
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
	want := []SkippedServer{{Agent: "Claude Code", Name: "github", Cause: SwitchedOffSkip{}}}
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

	want := []SkippedServer{{Agent: "Codex", Name: "notion", Cause: SwitchedOffSkip{}}}
	if !reflect.DeepEqual(report.Import.Skipped, want) || len(report.Import.DroppedSettings) != 0 {
		t.Errorf("skipped = %+v, dropped settings = %v; want Codex's notion named as switched off "+
			"and no cwd note for the catalog's notion", report.Import.Skipped, report.Import.DroppedSettings)
	}
}

func TestRunUnattended_aServerNeedingTheUsersCredentialsGetsThemCommentedOut(t *testing.T) {
	setup, report := addCredentialServers(t)

	github := string(testutil.ReadFile(t, config.ServerPath(setup.ConfigDir, "github")))
	if !strings.Contains(github, "# Create a token at https://github.example/tokens, then uncomment:") {
		t.Errorf("github.yaml:\n%s\nwant the token step as a comment", github)
	}
	if status := statusNamed(t, report.Servers, "github"); !status.NeedsOwnApp() {
		t.Errorf("github = %+v, want its own app: its authorization server lets no client register itself", status)
	}
	if strings.Contains(string(testutil.ReadFile(t, config.ServerPath(setup.ConfigDir, "linear"))), "#") {
		t.Error("linear.yaml has a comment, want none: its login needs nothing written by hand")
	}
}

func TestServerStatuses_aServerFinishedFromItsCommentedSteps(t *testing.T) {
	t.Run("github works once uncommented", func(t *testing.T) {
		setup, _ := addCredentialServers(t)
		if sc := uncommented(t, setup.ConfigDir, "github"); sc.Headers["Authorization"] != "Bearer <your token>" {
			t.Errorf("headers = %v, want the Authorization header", sc.Headers)
		}
	})
	t.Run("asana works once uncommented", func(t *testing.T) {
		setup, _ := addCredentialServers(t)
		if sc := uncommented(t, setup.ConfigDir, "asana"); sc.Auth == nil || sc.Auth.ClientID == "" {
			t.Errorf("auth = %+v, want the app's client ID", sc.Auth)
		}
	})
	t.Run("an uncommented placeholder is no credential until replaced", func(t *testing.T) {
		setup, _ := addCredentialServers(t)
		for name, want := range map[string]Readiness{"github": NeedsTokenState{}, "asana": NeedsOwnAppState{}} {
			uncommented(t, setup.ConfigDir, name)
			if status := currentStatus(t, setup, name); status.Readiness != want {
				t.Errorf("%s = %+v, want %v: mini would send the placeholder",
					name, status, want)
			}
		}
	})
	t.Run("an app with its client ID but the secret's placeholder isn't finished", func(t *testing.T) {
		setup, _ := addCredentialServers(t)
		uncommented(t, setup.ConfigDir, "asana")
		editServerFile(t, setup.ConfigDir, "asana", clientIDPlaceholder, "app-123")
		if status := currentStatus(t, setup, "asana"); !status.NeedsOwnApp() {
			t.Errorf("asana = %+v, want it still needing its app: the secret is a placeholder", status)
		}
	})
}

// addCredentialServers adds a token server, an OAuth-app server and a login server from the catalog.
func addCredentialServers(t *testing.T) (Setup, Report) {
	t.Helper()
	setup := Setup{ConfigDir: t.TempDir(), Add: []catalog.Entry{
		{
			Name:     "github",
			URL:      "https://github.example/mcp",
			Auth:     catalog.AuthToken,
			SetupURL: "https://github.example/tokens",
		},
		{
			Name:     "asana",
			URL:      "https://asana.example/mcp",
			Auth:     catalog.AuthOAuth2App,
			SetupURL: "https://asana.example/apps",
		},
		{Name: "linear", URL: "https://linear.example/mcp", Auth: catalog.AuthOAuth2},
	}}
	setup.Probe = answersWithOAuth
	setup.Discover = registersOnlyAt("https://linear.example/mcp")
	report := RunUnattended(setup)
	if len(report.WriteErrors) > 0 {
		t.Fatalf("write errors: %v", report.WriteErrors)
	}
	return setup, report
}

// answersWithOAuth stands in for a probe whose server answered with an OAuth challenge.
func answersWithOAuth(_ context.Context, configDir string, sc config.ServerConfig) error {
	return config.MarkOAuthDetected(configDir, sc.Name)
}

func registersOnlyAt(selfRegistering ...string) discoverFunc {
	return func(_ context.Context, serverURL string) (*auth.ServerMeta, error) {
		if slices.Contains(selfRegistering, serverURL) {
			return &auth.ServerMeta{RegistrationURL: serverURL + "/register"}, nil
		}
		return &auth.ServerMeta{}, nil
	}
}

func editServerFile(t *testing.T, configDir, name, old, replacement string) {
	t.Helper()
	path := config.ServerPath(configDir, name)
	testutil.WriteFile(t, path, strings.Replace(string(testutil.ReadFile(t, path)), old, replacement, 1))
}

func currentStatus(t *testing.T, setup Setup, name string) ServerStatus {
	t.Helper()
	statuses, err := ServerStatuses(setup.ConfigDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return statusNamed(t, statuses, name)
}

func statusNamed(t *testing.T, statuses []ServerStatus, name string) ServerStatus {
	t.Helper()
	i := slices.IndexFunc(statuses, func(s ServerStatus) bool { return s.Name == name })
	if i < 0 {
		t.Fatalf("statuses = %+v, want %s among them", statuses, name)
	}
	return statuses[i]
}

func TestRunUnattended_anImportThatCoversACatalogAddGetsNoSetupComment(t *testing.T) {
	configDir := t.TempDir()
	claude := agentWith("Claude Code", map[string]agents.Server{
		"github": remoteEntry("https://github.example/mcp", "ghp_test"),
	})
	report := RunUnattended(Setup{
		ConfigDir: configDir,
		Import:    []agents.Agent{claude},
		Add:       []catalog.Entry{{Name: "github", URL: "https://github.example/mcp", Auth: catalog.AuthToken}},
		Probe:     func(context.Context, string, config.ServerConfig) error { return nil },
	})

	if len(report.WriteErrors) > 0 {
		t.Fatalf("write errors: %v", report.WriteErrors)
	}
	if github := string(testutil.ReadFile(t, config.ServerPath(configDir, "github"))); strings.Contains(github, "#") {
		t.Errorf("github.yaml:\n%s\nwant no token step: the imported github already holds its token", github)
	}
}

// uncommented removes the "# " the setup comment's lines start with, as a user would, and loads the result.
func uncommented(t *testing.T, configDir, name string) config.ServerConfig {
	t.Helper()
	path := config.ServerPath(configDir, name)
	var lines []string
	for line := range strings.SplitSeq(string(testutil.ReadFile(t, path)), "\n") {
		if strings.HasPrefix(line, "# ") && !strings.HasSuffix(line, "uncomment:") {
			line = strings.TrimPrefix(line, "# ")
		}
		lines = append(lines, line)
	}
	testutil.WriteFile(t, path, strings.Join(lines, "\n"))
	sc, err := config.LoadServer(configDir, name)
	if err != nil {
		t.Fatalf("%s uncommented doesn't load: %v\n%s", name, err, strings.Join(lines, "\n"))
	}
	return sc
}
