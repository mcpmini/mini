package initcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

func TestSummary_servers(t *testing.T) {
	custom := agents.MiniEntry{Command: "/opt/mini", Args: []string{"--config", "/srv/my mini", "connect"}}
	t.Run("what is left, with the step that finishes each", func(t *testing.T) {
		got := plainSummary(Report{ConfigDir: "/cfg", Agents: AgentConnections{Mini: custom}, Added: []string{
			"github", "asana", "notion", "files",
		}, Servers: []ServerStatus{
			{Name: "github", Readiness: NeedsToken, SetupURL: "https://github.example.com/tokens"},
			{Name: "asana", Readiness: NeedsOwnApp, SetupURL: "https://asana.example.com/apps"},
			{Name: "notion", Readiness: NeedsLogin},
			{Name: "files", Readiness: Ready},
		}})
		requireLines(
			t,
			got,
			"Added 4 servers to mini: github, asana, notion, files\n\n"+
				"These servers need one more step before they work:\n",
			"  github  requires an access token  create a token at https://github.example.com/tokens, then add to /cfg/servers/github.yaml:\n",
			"  asana   requires an OAuth app     register an OAuth app at https://asana.example.com/apps",
			"client_id: <your app's client ID>",
			"then run: mini --config '/srv/my mini' auth asana\n",
			"  notion  requires a login          run: mini --config '/srv/my mini' auth notion\n",
		)
		if strings.Contains(got, "files  ") {
			t.Errorf("a ready server is listed:\n%s", got)
		}
	})
	t.Run("a server whose file opens with its steps points there", func(t *testing.T) {
		got := plainSummary(Report{ConfigDir: "/cfg", Servers: []ServerStatus{
			{Name: "github", Readiness: NeedsToken, StepsInFile: true},
			{Name: "asana", Readiness: NeedsOwnApp, StepsInFile: true},
		}})
		requireLines(
			t,
			got,
			"  github  requires an access token  finish the steps in /cfg/servers/github.yaml\n",
			"  asana   requires an OAuth app     finish the steps in /cfg/servers/asana.yaml, then run: mini auth asana\n",
		)
	})
	t.Run("the token step is YAML that sets the header", func(t *testing.T) {
		got := plainSummary(Report{ConfigDir: "/cfg", Servers: []ServerStatus{
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
			sc.Headers["Authorization"] != "Bearer <your token>" {
			t.Errorf("step %q parses to %+v, %v; want the Authorization header", lines, sc.Headers, err)
		}
	})
	t.Run("servers added are named in commit order", func(t *testing.T) {
		requireLines(
			t,
			plainSummary(Report{Added: []string{"github", "files"}}),
			"Added 2 servers to mini: github, files\n",
		)
	})
	t.Run("a server in mini that this run didn't add isn't counted as added", func(t *testing.T) {
		got := plainSummary(Report{Added: []string{"files"}, Servers: []ServerStatus{
			{Name: "files"}, {Name: "github", Readiness: NeedsLogin},
		}})
		requireLines(t, got, "Added 1 server to mini: files\n", "  github  requires a login")
	})
	t.Run("nothing added says so", func(t *testing.T) {
		requireLines(t, plainSummary(Report{}), "No servers were added to mini.\n")
	})
	t.Run("servers that can't be read aren't called none", func(t *testing.T) {
		got := plainSummary(Report{ReadServersErr: errors.New("permission denied")})
		if strings.Contains(got, "No servers were added") {
			t.Errorf("summary says nothing was added although it couldn't read mini's servers:\n%s", got)
		}
	})
}

func TestSummary_unfinishedRowsAlignAndWrapUnderTheStepColumn(t *testing.T) {
	got := plainSummary(Report{ConfigDir: "/cfg", Servers: []ServerStatus{
		{Name: "github", Readiness: NeedsToken, SetupURL: "https://github.example.com/tokens"},
		{Name: "asana", Readiness: NeedsOwnApp, StepsInFile: true},
		{Name: "notion", Readiness: NeedsLogin},
		{Name: "search", Readiness: NeedsEnv, UnsetEnv: &config.UnsetEnvError{
			Field: "headers.Authorization", Names: []string{"SEARCH_TOKEN"},
		}},
	}})
	requireLines(
		t,
		got,
		"  github  requires an access token  create a token at https://github.example.com/tokens, then add to /cfg/servers/github.yaml:\n",
		"  asana   requires an OAuth app     finish the steps in /cfg/servers/asana.yaml, then run: mini auth asana\n",
		"  notion  requires a login          run: mini auth notion\n",
		"  search  requires SEARCH_TOKEN     set it where mini runs (used in headers.Authorization), or edit /cfg/servers/search.yaml\n",
	)
	column := strings.Repeat(" ", 36)
	requireLines(t, got, column+"headers:\n"+column+"    Authorization: Bearer <your token>\n")
}

func TestSummary_tokenServerWithoutStepsInFileShowsTheBlock(t *testing.T) {
	got := plainSummary(Report{ConfigDir: "/cfg", Servers: []ServerStatus{
		{Name: "github", Readiness: NeedsToken, SetupURL: "https://github.example.com/tokens"},
	}})
	requireLines(t, got, "then add to /cfg/servers/github.yaml:\n"+strings.Repeat(" ", 36)+"headers:\n")
}

func TestSummary_importAndFailures(t *testing.T) {
	got := plainSummary(Report{
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
	got := plainSummary(Report{Agents: AgentConnections{Mini: mini, NoMini: list}})
	requireLines(t, got,
		"claude mcp add --scope user mini -- '/Users/u/My Apps/mini' connect\n",
		"[mcp_servers.mini]\n    command = \"/Users/u/My Apps/mini\"\n    args = [\"connect\"]\n",
		`"command": "/Users/u/My Apps/mini"`,
	)
	if strings.Contains(got, "Restart") {
		t.Errorf("nothing was connected, but the summary asks for a restart:\n%s", got)
	}
	if summary := plainSummary(Report{Agents: AgentConnections{Mini: mini, MiniServes: list[:1]}}); strings.Contains(
		summary,
		"To connect mini",
	) {
		t.Errorf("every agent already has mini, but the summary shows how to connect one:\n%s", summary)
	}
	got = plainSummary(Report{Agents: AgentConnections{Mini: mini, MiniInactive: list[2:]}})
	requireLines(t, got, "Cursor (/home/u/.cursor/mcp.json) has a mini entry that may not run these servers: "+
		"it's switched off, uses another config directory, or doesn't start mini connect by absolute path. "+
		"To use them, have it run: '/Users/u/My Apps/mini' connect\n")
	if strings.Contains(got, "To connect mini") {
		t.Errorf("the only agent has a mini entry, but the summary adds a generic step:\n%s", got)
	}
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
	t.Run("agents that were found but not connected keep their hand steps", func(t *testing.T) {
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

func TestSummary_styleWrapsWarningAndCommandsOnly(t *testing.T) {
	report := Report{
		ConfigDir: "/cfg",
		Agents:    AgentConnections{Mini: agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect"}}},
		Servers: []ServerStatus{
			{Name: "github", Readiness: NeedsToken, SetupURL: "https://github.example.com/tokens"},
		},
	}
	styled := Summary(report, SummaryStyle{Warning: wrapWith("!"), Code: wrapWith("`")})
	requireLines(
		t,
		styled,
		"!⚠ No agents were found automatically!\n",
		"`/cfg/servers/github.yaml`",
		"command: `/opt/mini connect`\n",
	)
	if plain := plainSummary(report); strings.ContainsAny(plain, "!`") {
		t.Errorf("zero style marked text:\n%s", plain)
	}
}

func wrapWith(mark string) func(string) string {
	return func(s string) string { return mark + s + mark }
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/mini": "/usr/bin/mini", "a b": "'a b'", "it's": `'it'\''s'`, "/opt/$HOME/mini": "'/opt/$HOME/mini'"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSummary_connectedAgents(t *testing.T) {
	mini := agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect"}}
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	cursor := agents.Agent{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"}
	codex := agents.Agent{Name: "Codex", ConfigPath: "/home/u/.codex/config.toml"}
	got := plainSummary(Report{Agents: AgentConnections{Mini: mini}, Connected: []AgentResult{
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
	got := plainSummary(Report{
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

func TestSummary_saysWhatRemovingTookOutAndWhyTheRestStayed(t *testing.T) {
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	mini := agents.MiniEntry{Command: "/opt/mini", Args: []string{"connect", "--config", "/home/u/m"}}
	got := plainSummary(Report{
		Servers: []ServerStatus{{Name: "linear", Readiness: NeedsLogin}, {Name: "github"}, {Name: "slow"}},
		Agents:  AgentConnections{Mini: mini},
		Connected: []AgentResult{{
			Agent:   claude,
			Backup:  "/home/u/.claude.minibackup.json",
			Removed: []string{"github", "notion"},
			Kept: []KeptEntry{
				{Entry: "lin", Server: "linear", Err: errors.New("401 unauthorized")},
				{Entry: "npx-slow", Server: "slow", Err: fmt.Errorf("initialize: %w", context.DeadlineExceeded)},
				{Entry: "gh", Server: "github", Err: errors.New("connection refused")},
				{Entry: "files", Server: "files", Err: errNotChecked},
				{Entry: "docs", Server: "docs", Err: errNotCounted},
			},
			Changed: []string{"notes"},
		}},
	})
	requireLines(t, got,
		"Claude Code: /home/u/.claude.json backed up to /home/u/.claude.minibackup.json\n"+
			"  removed github and notion, which mini runs now\n",
		"  lin stays in Claude Code: it works in mini once you finish linear above\n",
		"  npx-slow stays in Claude Code: mini's check timed out; mini --config /home/u/m test tries again\n",
		"  gh stays in Claude Code: mini couldn't connect to it; mini --config /home/u/m test shows why\n",
		"  files stays in Claude Code: its connection wasn't checked\n",
		"  docs stays in Claude Code: it wasn't among the MCPs you chose to remove\n",
		"  notes stays in Claude Code: it changed after it was checked\n",
	)
}

func TestSummary_anAgentThatSwitchesRemovedEntriesOffSaysSo(t *testing.T) {
	codex := agents.Agent{Name: "Codex", ConfigPath: "/home/u/.codex/config.toml", RemoveDisables: true}
	got := plainSummary(Report{Connected: []AgentResult{{
		Agent: codex, Backup: "/home/u/.codex/config.minibackup.toml", Removed: []string{"github"},
	}}})
	requireLines(t, got, "  switched off github, which mini runs now\n")
}

func TestSummary_anAgentThatAlreadyHadMiniSaysSo(t *testing.T) {
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	got := plainSummary(
		Report{Agents: AgentConnections{Mini: agents.MiniEntry{Command: "/opt/mini"}}, Connected: []AgentResult{{
			Agent:        claude,
			ExistingMini: MiniEntryServes,
			MiniServes:   true,
		}}},
	)
	requireLines(t, got, "\nClaude Code: already has a mini entry; /home/u/.claude.json is unchanged\n")
}
