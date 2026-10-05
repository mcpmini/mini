//go:build test

package initcmd

import (
	"errors"
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

func TestRunFlags(t *testing.T) {
	f := newApplyFixture(t)
	configtest.WriteServer(t, f.configDir, config.ServerConfig{Name: "linear", Transport: "http", URL: "https://linear.example.com/mcp"})
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

	report := RunFlags(FlagRun{ConfigDir: f.configDir, Import: []agents.Agent{claude, codex}, Add: add, SelfPath: testSelf})

	if got := configuredNames(t, f.configDir); !reflect.DeepEqual(got, []string{"files", "github", "linear", "notion"}) {
		t.Errorf("configured = %v, want files and github imported, notion added, and no switched-off server", got)
	}
	if !reflect.DeepEqual(report.AlreadyConfigured, []string{"linear"}) {
		t.Errorf("already configured = %v, want linear", report.AlreadyConfigured)
	}
	if !reflect.DeepEqual(report.FromImport, []string{"files", "github"}) {
		t.Errorf("from import = %v, want files and github, which the imports cover", report.FromImport)
	}
	wantSkipped := []SkippedServer{{Agent: "Claude Code", Name: "paused", Reason: SkipSwitchedOff}}
	if !reflect.DeepEqual(report.Skipped, wantSkipped) || !reflect.DeepEqual(report.Ignored, map[string][]string{"files": {"cwd"}}) || report.Failed() {
		t.Errorf("skipped = %+v, ignored = %v, failed = %v; want paused named as switched off, files' cwd named, no failure",
			report.Skipped, report.Ignored, report.Failed())
	}
	github, err := config.ReadUnexpandedServer(f.configDir, "github")
	if err != nil || github.Headers["Authorization"] != "Bearer ${GH}" {
		t.Errorf("github = %+v, %v; want the imported copy, which carries the header", github, err)
	}
}

func TestRunFlags_namesASecondConfigItLeavesOut(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(t, "Claude Code", `{"mcpServers":{"github":{"command":"gh-server-a"}}}`)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"github":{"command":"gh-server-b"}}}`)

	report := RunFlags(FlagRun{ConfigDir: f.configDir, Import: []agents.Agent{claude, cursor}, SelfPath: testSelf})

	want := []SkippedServer{{Agent: "Cursor", Name: "github", Reason: SkipSecondConfig}}
	if !reflect.DeepEqual(report.Skipped, want) || !reflect.DeepEqual(configuredNames(t, f.configDir), []string{"github"}) {
		t.Errorf("skipped = %+v, configured = %v; want Claude Code's github imported and Cursor's named", report.Skipped, configuredNames(t, f.configDir))
	}
}

func TestRunFlags_anEnabledConfigWinsANameFromASwitchedOffOne(t *testing.T) {
	f := newApplyFixture(t)
	claude := f.write(t, "Claude Code", `{"mcpServers":{"github":{"command":"gh-server-a","disabled":true}}}`)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"github":{"command":"gh-server-b"}}}`)

	report := RunFlags(FlagRun{ConfigDir: f.configDir, Import: []agents.Agent{claude, cursor}, SelfPath: testSelf})

	github, err := config.ReadUnexpandedServer(f.configDir, "github")
	if err != nil || github.Command != "gh-server-b" {
		t.Errorf("github = %+v, %v; want Cursor's enabled config imported", github, err)
	}
	want := []SkippedServer{{Agent: "Claude Code", Name: "github", Reason: SkipSwitchedOff}}
	if !reflect.DeepEqual(report.Skipped, want) {
		t.Errorf("skipped = %+v, want Claude Code's switched-off github named", report.Skipped)
	}
}

func TestRunFlags_sortsAgentsByTheirMiniEntry(t *testing.T) {
	f := newApplyFixture(t)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"mini":{"command":"/opt/old/mini","args":["connect"],"disabled":true}}}`)
	windsurf := f.write(t, "Windsurf", `{"mcpServers":{"proxy":`+f.servingMini()+`}}`)
	claude := f.write(t, "Claude Code", `{"mcpServers":{}}`)

	report := RunFlags(FlagRun{ConfigDir: f.configDir, Connectable: []agents.Agent{cursor, windsurf, claude}, SelfPath: testSelf})

	for _, group := range []struct {
		name string
		got  []agents.Agent
		want []string
	}{
		{"unconnected", report.Unconnected, []string{"Claude Code"}},
		{"has a serving mini", report.HasMini, []string{"Windsurf"}},
		{"has an inactive mini", report.InactiveMini, []string{"Cursor"}},
	} {
		if got := agentNames(group.got); !reflect.DeepEqual(got, group.want) {
			t.Errorf("%s = %v, want %v", group.name, got, group.want)
		}
	}
}

func TestReport_failed(t *testing.T) {
	for name, tt := range map[string]struct {
		report Report
		want   bool
	}{
		"nothing failed":        {Report{Connected: []AgentResult{{}}}, false},
		"a server write failed": {Report{Sync: SyncResult{Failed: []ServerError{{Name: "x", Err: errors.New("disk full")}}}}, true},
		"servers can't be read": {Report{StatusErr: errors.New("permission denied")}, true},
		"an agent edit failed":  {Report{Connected: []AgentResult{{Err: errors.New("inline table")}}}, true},
	} {
		if got := tt.report.Failed(); got != tt.want {
			t.Errorf("%s: Failed() = %v, want %v", name, got, tt.want)
		}
	}
}

func TestRunFlags_addAloneImportsNothing(t *testing.T) {
	f := newApplyFixture(t)
	f.write(t, "Claude Code", `{"mcpServers":{"files":{"command":"files-server"}}}`)

	RunFlags(FlagRun{ConfigDir: f.configDir, Add: []catalog.Entry{{Name: "notion", URL: "https://notion.example.com/mcp"}}})

	if got := configuredNames(t, f.configDir); !reflect.DeepEqual(got, []string{"notion"}) {
		t.Errorf("configured = %v, want only notion", got)
	}
}
