//go:build test

package initcmd

import (
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
	if report.Skipped != nil || !reflect.DeepEqual(report.Ignored, map[string][]string{"files": {"cwd"}}) || report.Failed() {
		t.Errorf("skipped = %+v, ignored = %v, failed = %v; want files imported with its cwd named and no failure",
			report.Skipped, report.Ignored, report.Failed())
	}
	github, err := config.ReadUnexpandedServer(f.configDir, "github")
	if err != nil || github.Headers["Authorization"] != "Bearer ${GH}" {
		t.Errorf("github = %+v, %v; want the imported copy, which carries the header", github, err)
	}
}

func TestRunFlags_agentsThatHaveMiniGetNoConnectStep(t *testing.T) {
	f := newApplyFixture(t)
	cursor := f.write(t, "Cursor", `{"mcpServers":{"mini":{"command":"/opt/old/mini","args":["connect"],"disabled":true}}}`)
	claude := f.write(t, "Claude Code", `{"mcpServers":{}}`)

	report := RunFlags(FlagRun{ConfigDir: f.configDir, Connectable: []agents.Agent{cursor, claude}, SelfPath: testSelf})

	if got := agentNames(report.Unconnected); !reflect.DeepEqual(got, []string{"Claude Code"}) {
		t.Errorf("unconnected = %v, want only Claude Code: Cursor's mini entry is the user's", got)
	}
	if got := agentNames(report.HasMini); !reflect.DeepEqual(got, []string{"Cursor"}) {
		t.Errorf("has mini = %v, want Cursor", got)
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
