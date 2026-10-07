package initcmd

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

func requireLines(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(got, line) {
			t.Errorf("summary missing %q:\n%s", line, got)
		}
	}
}

func TestSummary_servers(t *testing.T) {
	custom := agents.MiniEntry{Command: "/opt/mini", Args: []string{"--config", "/srv/my mini", "connect"}}
	t.Run("what is left, with the step that finishes each", func(t *testing.T) {
		got := Summary(Report{ConfigDir: "/cfg", Agents: AgentConnections{Mini: custom}, Servers: []ServerStatus{
			{Name: "github", Readiness: NeedsToken, SetupURL: "https://github.example.com/tokens"},
			{Name: "asana", Readiness: NeedsOwnApp, SetupURL: "https://asana.example.com/apps"},
			{Name: "notion", Readiness: NeedsLogin},
			{Name: "plain", Readiness: MayNeedLogin},
			{Name: "files", Readiness: Ready},
		}})
		requireLines(
			t,
			got,
			"mini is set up with 5 servers, 4 still need finishing:\n",
			"  github  needs a token: create one at https://github.example.com/tokens, then add to /cfg/servers/github.yaml:\n",
			"  asana   needs your own OAuth app: register one at https://asana.example.com/apps",
			"client_id: <your app's client ID>",
			"and run: mini --config '/srv/my mini' auth asana\n",
			"  notion  run: mini --config '/srv/my mini' auth notion\n",
			"  plain   wasn't checked for a login; if it asks for one, run: mini --config '/srv/my mini' auth plain\n",
		)
		if strings.Contains(got, "files") {
			t.Errorf("a ready server is listed:\n%s", got)
		}
	})
	t.Run("the token step is YAML that sets the header", func(t *testing.T) {
		got := Summary(Report{ConfigDir: "/cfg", Servers: []ServerStatus{
			{Name: "github", Readiness: NeedsToken, SetupURL: "https://github.example.com/tokens"},
		}})
		_, step, _ := strings.Cut(got, "github.yaml:\n")
		lines := strings.SplitN(step, "\n", 3)[:2]
		indent := len(lines[0]) - len(strings.TrimLeft(lines[0], " "))
		var sc config.ServerConfig
		if err := yaml.Unmarshal(
			[]byte(lines[0][indent:]+"\n"+lines[1][indent:]),
			&sc,
		); err != nil ||
			sc.Headers["Authorization"] != "Bearer ${GITHUB_TOKEN}" {
			t.Errorf("step %q parses to %+v, %v; want the Authorization header", lines, sc.Headers, err)
		}
	})
	t.Run("nothing left", func(t *testing.T) {
		got := Summary(Report{Servers: []ServerStatus{{Name: "files"}}})
		requireLines(t, got, "mini is set up with 1 server.\n")
	})
	t.Run("no servers", func(t *testing.T) {
		requireLines(t, Summary(Report{}), "mini has no servers yet.\n")
	})
	t.Run("servers that can't be read aren't called none", func(t *testing.T) {
		got := Summary(Report{ReadServersErr: errors.New("permission denied")})
		if strings.Contains(got, "no servers yet") {
			t.Errorf("summary says there are no servers although it couldn't read them:\n%s", got)
		}
	})
}

func TestSummary_importAndFailures(t *testing.T) {
	got := Summary(Report{
		AlreadyConfigured:  []string{"linear"},
		AddCoveredByImport: []string{"github"},
		Import: ImportPlan{
			Skipped: []SkippedServer{
				{
					Agent:  "Codex",
					Name:   "templated",
					Reason: SkipUnexpandableRefs,
					Refs:   []string{"an environment variable in url"},
				},
				{Agent: "Cursor", Name: "!!!", Reason: SkipEmptyName},
				{Agent: "Codex", Name: "paused", Reason: SkipSwitchedOff},
				{Agent: "Cursor", Name: "github", Reason: SkipSecondConfig},
				{Agent: "Codex", Name: "Linear", Reason: SkipNameInMini},
			},
			DroppedSettings: map[string][]string{"files": {"cwd"}},
			StaticHeaders:   map[string]map[string]string{"team": {"X-Team": "TEAM_VAR"}},
			Unreadable: []UnreadableAgent{
				{Agent: "Cursor", ConfigPath: "/home/.cursor/mcp.json", Err: errors.New("invalid character")},
			},
		},
		ConfigDir:      "/config",
		WriteErrors:    []ServerError{{Name: "broken", Err: errors.New("disk full")}},
		ReadServersErr: errors.New("permission denied"),
	})
	requireLines(
		t,
		got,
		"Already configured in mini: linear\n",
		"Imported from your agents instead of the catalog: github\n",
		"  paused switched off in Codex\n",
		"  github in Cursor: a different config under that name is imported instead\n",
		"  Linear in Codex: mini already has a different linear; to use this one, edit /config/servers/linear.yaml\n",
		"files was imported without its cwd, which mini doesn't support yet; if it fails to start, edit /config/servers/files.yaml\n",
		"team was imported with its static X-Team header, since TEAM_VAR wasn't set; to use TEAM_VAR instead, set X-Team: ${TEAM_VAR} in /config/servers/team.yaml\n",
		"  templated kept in Codex: uses an environment variable in url\n",
		`  "!!!" in Cursor: its name has no letters or digits mini can use`,
		"Could not add broken: disk full\n",
		"Could not read Cursor's config (/home/.cursor/mcp.json), so none of its servers were imported: invalid character\n",
		"Could not read mini's servers: permission denied\n",
	)
}

func TestSummary_howToConnectByHand(t *testing.T) {
	list := []agents.Agent{
		{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"},
		{Name: "Codex", ConfigPath: "/home/u/.codex/config.toml"},
		{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"},
	}
	mini := agents.MiniEntry{Command: "/Users/u/My Apps/mini", Args: []string{"connect"}}
	got := Summary(Report{Agents: AgentConnections{Mini: mini, NoMini: list}})
	requireLines(t, got,
		"claude mcp add --scope user mini -- '/Users/u/My Apps/mini' connect\n",
		"[mcp_servers.mini]\n    command = \"/Users/u/My Apps/mini\"\n    args = [\"connect\"]\n",
		`"command": "/Users/u/My Apps/mini"`,
	)
	if strings.Contains(got, "Restart") {
		t.Errorf("nothing was connected, but the summary asks for a restart:\n%s", got)
	}
	requireLines(
		t,
		Summary(Report{Agents: AgentConnections{Mini: mini}}),
		"To connect mini to your agent, add it to its MCP config:\n",
	)
	if got := Summary(
		Report{Agents: AgentConnections{Mini: mini, MiniServes: list[:1]}},
	); strings.Contains(
		got,
		"To connect mini",
	) {
		t.Errorf("every agent already has mini, but the summary shows how to connect one:\n%s", got)
	}
	got = Summary(Report{Agents: AgentConnections{Mini: mini, MiniInactive: list[2:]}})
	requireLines(t, got, "Cursor (/home/u/.cursor/mcp.json) has a mini entry that may not run these servers: "+
		"it's switched off, uses another config directory, or doesn't name mini by absolute path. "+
		"To use them, have it run: '/Users/u/My Apps/mini' connect\n")
	if strings.Contains(got, "To connect mini") {
		t.Errorf("the only agent has a mini entry, but the summary adds a generic step:\n%s", got)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/mini": "/usr/bin/mini", "a b": "'a b'", "it's": `'it'\''s'`, "/opt/$HOME/mini": "'/opt/$HOME/mini'"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandConnectSteps(t *testing.T) {
	f := newApplyFixture(t)
	cursor := f.write(t, "Cursor", `{"mcpServers":{}}`)
	windsurf := f.write(t, "Windsurf", `{"mcpServers":{"proxy":`+f.servingMini()+`}}`)

	got := HandConnectSteps(f.configDir, testSelf, []agents.Agent{cursor, windsurf})

	requireLines(t, got, "  Cursor ("+cursor.ConfigPath+"):\n")
	if !strings.Contains(got, f.configDir) || strings.Contains(got, "Windsurf") {
		t.Errorf(
			"steps:\n%s\nwant Cursor's step running this config directory and none for Windsurf, which already runs mini",
			got,
		)
	}
}

func TestSummary_connectedAgents(t *testing.T) {
	mini := agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect"}}
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	cursor := agents.Agent{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"}
	codex := agents.Agent{Name: "Codex", ConfigPath: "/home/u/.codex/config.toml"}
	got := Summary(Report{Agents: AgentConnections{Mini: mini}, Connected: []AgentResult{
		{Agent: claude, Backup: "/home/u/.claude.minibackup.json"},
		{Agent: cursor, Created: true},
		{Agent: codex, Err: errors.New("parse config.toml: bad table")},
	}})
	requireLines(t, got,
		"Claude Code: /home/u/.claude.json backed up to /home/u/.claude.minibackup.json\n",
		"Cursor: created /home/u/.cursor/mcp.json\n",
		"Couldn't connect Codex: parse config.toml: bad table\nAdd mini to /home/u/.codex/config.toml by hand:\n"+
			"  [mcp_servers.mini]\n",
		"Restart Claude Code and Cursor to start using mini.\n",
	)
	if strings.Contains(got, "To connect mini to your agents") {
		t.Errorf("summary:\n%s\nwant no hand steps: every agent was tried", got)
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

func TestSummary_anAgentLeftUntickedKeepsItsHandStep(t *testing.T) {
	mini := agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect"}}
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	cursor := agents.Agent{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"}
	got := Summary(Report{
		Agents:    AgentConnections{Mini: mini, MiniServes: []agents.Agent{claude}, NoMini: []agents.Agent{cursor}},
		Connected: []AgentResult{{Agent: claude, Backup: "/home/u/.claude.minibackup.json"}},
	})
	requireLines(t, got, "To connect mini to your agents:\n  Cursor (/home/u/.cursor/mcp.json):\n")
	if strings.Contains(got, "Claude Code (/home/u/.claude.json)") {
		t.Errorf("summary:\n%s\nwant no hand step for Claude Code, which was connected", got)
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

func TestSummary_entriesRemovingLeftInPlace(t *testing.T) {
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	got := Summary(
		Report{Agents: AgentConnections{Mini: agents.MiniEntry{Command: "/opt/mini"}}, Connected: []AgentResult{{
			Agent:  claude,
			Backup: "/home/u/.claude.minibackup.json",
			Kept: []KeptEntry{
				{Entry: "gh", Server: "github", Err: errors.New("connection refused")},
				{Entry: "files", Server: "files", Err: errNotChecked},
			},
			Changed: []string{"notes"},
		}}},
	)
	requireLines(t, got,
		"  gh stays in Claude Code: mini's github failed its connection check: connection refused\n",
		"  files stays in Claude Code: its connection wasn't checked\n",
		"  notes stays in Claude Code: it changed after it was checked\n",
	)
}

func TestSummary_anAgentWhoseEntriesAllStayedStillGetsAHeading(t *testing.T) {
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	got := Summary(
		Report{Agents: AgentConnections{Mini: agents.MiniEntry{Command: "/opt/mini"}}, Connected: []AgentResult{{
			Agent: claude,
			Kept:  []KeptEntry{{Entry: "gh", Server: "github", Err: errors.New("connection refused")}},
		}}},
	)
	requireLines(t, got, "\nClaude Code: /home/u/.claude.json is unchanged\n  gh stays in Claude Code")
}
