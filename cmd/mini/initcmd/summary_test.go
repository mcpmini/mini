package initcmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

func plainSummary(r Report) string {
	return Summary(r, SummaryStyle{})
}

func requireLines(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(got, line) {
			t.Errorf("summary missing %q:\n%s", line, got)
		}
	}
}

func TestSummary_headline(t *testing.T) {
	t.Run("names what this run added, in commit order", func(t *testing.T) {
		connected := AgentConnections{MiniServes: []agents.Agent{{Name: "Claude Code"}}}
		got := plainSummary(Report{Added: []string{"github", "files"}, Interactive: true, Agents: connected})
		if got != "Added 2 MCPs to mini: github and files.\n" {
			t.Errorf("summary = %q, want only the headline", got)
		}
	})
	t.Run("nothing added says so", func(t *testing.T) {
		requireLines(t, plainSummary(Report{Interactive: true}), "No MCPs were added to mini.\n")
	})
	t.Run("a read error still says what this run added, then the error", func(t *testing.T) {
		got := plainSummary(Report{ReadServersErr: errors.New("permission denied")})
		requireLines(t, got, "No MCPs were added to mini.\nCould not read mini's servers: permission denied\n")
	})
}

func TestSummary_aRunWithoutScreensListsWhatNoScreenShowed(t *testing.T) {
	custom := agents.MiniEntry{Command: "/opt/mini", Args: []string{"--config", "/srv/my mini", "connect"}}
	report := Report{
		ConfigDir: "/cfg",
		Agents:    AgentConnections{Mini: custom, MiniServes: []agents.Agent{{Name: "Claude Code"}}},
		Added:     []string{"github", "asana", "notion", "files", "search"},
		Servers: []ServerStatus{
			{Name: "github", Readiness: NeedsToken},
			{Name: "asana", Readiness: NeedsOwnApp},
			{Name: "notion", Readiness: NeedsLogin},
			{Name: "files", Readiness: Ready},
			{Name: "search", Readiness: NeedsEnv, UnsetEnv: &config.UnsetEnvError{Names: []string{"TOKEN"}}},
		},
		AlreadyConfigured:  []string{"linear"},
		AddCoveredByImport: []string{"slack"},
		Import: ImportPlan{Skipped: []SkippedServer{
			{Agent: "Codex", Name: "paused", Reason: SkipSwitchedOff},
			{Agent: "Codex", Name: "Linear", Reason: SkipNameInMini},
		}},
	}
	requireLines(
		t,
		plainSummary(report),
		"These MCPs need one more step:\n",
		"  github  requires manual configuration, edit /cfg/servers/github.yaml\n",
		"  asana   requires a custom OAuth app, edit /cfg/servers/asana.yaml\n",
		"  notion  requires a login, run: mini --config '/srv/my mini' auth notion\n",
		"  search  requires manual configuration, edit /cfg/servers/search.yaml\n",
		"Already configured in mini: linear\n",
		"Imported from your agents instead of the catalog: slack\n",
		"Not imported: paused switched off in Codex\n",
		"Not imported: Linear in Codex: mini already has a different linear; to use this one, edit /cfg/servers/linear.yaml\n",
	)
	if got := plainSummary(report); strings.Contains(got, "  files") {
		t.Errorf("a ready server is listed:\n%s", got)
	}
	report.Interactive = true
	if got := plainSummary(report); got != "Added 5 MCPs to mini: github, asana, notion, files and search.\n" {
		t.Errorf("after the screens ran, summary = %q, want only the headline: they showed the rest", got)
	}
}

func TestSummary_importNotesAndFailuresShowEvenAfterTheScreens(t *testing.T) {
	got := plainSummary(Report{
		Interactive: true,
		ConfigDir:   "/config",
		Import: ImportPlan{
			DroppedSettings: map[string][]string{"files": {"cwd"}},
			StaticHeaders:   map[string]map[string]string{"team": {"X-Team": "TEAM_VAR"}},
			Unreadable: []UnreadableAgent{
				{Agent: "Cursor", ConfigPath: "/home/.cursor/mcp.json", Err: errors.New("invalid character")},
			},
		},
		WriteErrors: []ServerError{{Name: "broken", Err: errors.New("disk full")}},
	})
	requireLines(
		t,
		got,
		"files was imported without its cwd, which mini doesn't support yet; if it fails to start, edit /config/servers/files.yaml\n",
		"team was imported with its static X-Team header, since TEAM_VAR wasn't set; to use TEAM_VAR instead, set X-Team: ${TEAM_VAR} in /config/servers/team.yaml\n",
		"Could not read Cursor's config (/home/.cursor/mcp.json), so none of its MCPs were imported: invalid character\n",
		"Could not add broken: disk full\n",
	)
}

func TestSummary_agentsNotConnectedAreToldToRunInitAgain(t *testing.T) {
	mini := agents.MiniEntry{Command: "/opt/mini", Args: []string{"--config", "/srv/my mini", "connect"}}
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	cursor := agents.Agent{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"}
	got := plainSummary(
		Report{Interactive: true, Agents: AgentConnections{Mini: mini, NoMini: []agents.Agent{claude, cursor}}},
	)
	requireLines(t, got, "To connect mini to Claude Code and Cursor later, run: mini --config '/srv/my mini' init\n")
	if strings.Contains(got, "Restart") {
		t.Errorf("nothing was connected, but the summary asks for a restart:\n%s", got)
	}
	allConnected := plainSummary(Report{Agents: AgentConnections{Mini: mini, MiniServes: []agents.Agent{claude}}})
	if strings.Contains(allConnected, "To connect") {
		t.Errorf("every agent already has mini, but the summary says to connect one:\n%s", allConnected)
	}
	got = plainSummary(Report{Agents: AgentConnections{Mini: mini, MiniInactive: []agents.Agent{cursor}}})
	requireLines(t, got, "Cursor (/home/u/.cursor/mcp.json) has a mini entry that may not run these servers: ")
}

func TestSummary_connectedAgents(t *testing.T) {
	mini := agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect"}}
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	cursor := agents.Agent{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"}
	codex := agents.Agent{Name: "Codex", ConfigPath: "/home/u/.codex/config.toml"}
	desktop := agents.Agent{Name: "Claude Desktop", ConfigPath: "/home/u/desktop.json"}
	got := plainSummary(Report{
		Interactive: true,
		Agents:      AgentConnections{Mini: mini, NoMini: []agents.Agent{desktop}},
		Connected: []AgentResult{
			{Agent: claude, Backup: "/home/u/.claude.minibackup.json", Removed: []string{"github"}},
			{Agent: cursor, Created: true},
			{Agent: codex, Err: errors.New("parse config.toml: bad table")},
		},
	})
	want := "No MCPs were added to mini.\n" +
		"Claude Code's config was backed up to /home/u/.claude.minibackup.json\n" +
		"Could not connect mini to Codex: parse config.toml: bad table\n" +
		"Connected mini to Claude Code and Cursor. Restart them to start using mini.\n" +
		"To connect mini to Claude Desktop later, run: mini init\n"
	if got != want {
		t.Errorf("summary:\n%s\nwant:\n%s", got, want)
	}
}

func TestSummary_styleWrapsWarningAndCommandsOnly(t *testing.T) {
	report := Report{
		Interactive: true,
		Agents:      AgentConnections{Mini: agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect"}}},
	}
	styled := Summary(report, SummaryStyle{Warning: wrapWith("!"), Code: wrapWith("`")})
	requireLines(t, styled, "!⚠ No agents were found automatically!\n", "command: `/opt/mini connect`\n")
	if plain := plainSummary(report); strings.ContainsAny(plain, "!`") {
		t.Errorf("zero style marked text:\n%s", plain)
	}
}

func wrapWith(mark string) func(string) string {
	return func(s string) string { return mark + s + mark }
}

func TestSummary_noAgentsFoundGivesTheCommandToAddMini(t *testing.T) {
	const noAgents = "⚠ No agents were found automatically\n" +
		"To use mini, add it to your agent as an MCP server with the command: "
	entry := agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect"}}
	t.Run("the command is the entry agents run, with mini's full path", func(t *testing.T) {
		c := AgentConnections{Mini: entry, MiniName: "mini"}
		requireLines(t, plainSummary(Report{Agents: c}), noAgents+"/opt/mini connect\n")
	})
	t.Run("a config dir other than the default is passed", func(t *testing.T) {
		custom := agents.MiniEntry{Command: "/opt/mini", Args: []string{"--config", "/srv/my mini", "connect"}}
		want := noAgents + "/opt/mini --config '/srv/my mini' connect\n"
		requireLines(t, plainSummary(Report{Agents: AgentConnections{Mini: custom}}), want)
	})
	t.Run("agents that were found but not connected get no no-agents warning", func(t *testing.T) {
		claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
		cursor := agents.Agent{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"}
		for name, c := range map[string]AgentConnections{
			"with mini":    {Mini: entry, MiniServes: []agents.Agent{claude}},
			"without mini": {Mini: entry, NoMini: []agents.Agent{cursor}},
		} {
			if got := plainSummary(Report{Agents: c}); strings.Contains(got, "No agents were found") {
				t.Errorf("%s: summary:\n%s\nwant no no-agents warning: an agent was found", name, got)
			}
		}
	})
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/mini": "/usr/bin/mini", "a b": "'a b'", "it's": `'it'\''s'`, "/opt/$HOME/mini": "'/opt/$HOME/mini'"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinAnd(t *testing.T) {
	for want, names := range map[string][]string{
		"":           nil,
		"a":          {"a"},
		"a and b":    {"a", "b"},
		"a, b and c": {"a", "b", "c"},
	} {
		if got := JoinAnd(names); got != want {
			t.Errorf("JoinAnd(%q) = %q, want %q", names, got, want)
		}
	}
}

func TestReport_failsWhenAnAgentCouldNotBeConnected(t *testing.T) {
	failed := Report{
		Connected: []AgentResult{{Agent: agents.Agent{Name: "Codex"}, Err: errors.New("permission denied")}},
	}
	if !failed.Failed() {
		t.Error("a run that couldn't connect Codex reads as finished, so init exits 0")
	}
	if (Report{Connected: []AgentResult{{Agent: agents.Agent{Name: "Codex"}}}}).Failed() {
		t.Error("a run that connected Codex reads as failed")
	}
}
