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
		got := Summary(Report{ConfigDir: "/cfg", Mini: custom, Connected: []AgentResult{}, Servers: []ServerStatus{
			{Name: "github", Finish: NeedsToken, SetupURL: "https://github.example.com/tokens"},
			{Name: "asana", Finish: NeedsOwnApp, SetupURL: "https://asana.example.com/apps"},
			{Name: "notion", Finish: NeedsLogin},
			{Name: "files", Finish: Ready},
		}})
		requireLines(
			t,
			got,
			"mini is set up with 4 servers, 3 still need finishing:\n",
			"  github  needs a token: create one at https://github.example.com/tokens, then add to /cfg/servers/github.yaml:\n",
			"  asana   needs your own OAuth app: register one at https://asana.example.com/apps",
			"client_id: <your app's client ID>",
			"and run: mini --config '/srv/my mini' auth asana\n",
			"  notion  run: mini --config '/srv/my mini' auth notion\n",
		)
		if strings.Contains(got, "files") {
			t.Errorf("a ready server is listed:\n%s", got)
		}
	})
	t.Run("the token step is YAML that sets the header", func(t *testing.T) {
		got := Summary(Report{ConfigDir: "/cfg", Connected: []AgentResult{}, Servers: []ServerStatus{
			{Name: "github", Finish: NeedsToken, SetupURL: "https://github.example.com/tokens"},
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
		got := Summary(Report{Connected: []AgentResult{}, Servers: []ServerStatus{{Name: "files"}}})
		requireLines(t, got, "mini is set up with 1 server.\n")
	})
	t.Run("no servers", func(t *testing.T) {
		requireLines(t, Summary(Report{Connected: []AgentResult{}}), "mini has no servers yet.\n")
	})
	t.Run("servers that can't be read aren't called none", func(t *testing.T) {
		got := Summary(Report{Connected: []AgentResult{}, StatusErr: errors.New("permission denied")})
		if strings.Contains(got, "no servers yet") {
			t.Errorf("summary says there are no servers although it couldn't read them:\n%s", got)
		}
	})
}

var testMini = agents.MiniEntry{Command: "/opt/mini/bin/mini", Args: []string{"connect"}}

func TestSummary_importAndFailures(t *testing.T) {
	got := Summary(Report{
		Connected:         []AgentResult{},
		AlreadyConfigured: []string{"linear"},
		FromImport:        []string{"github"},
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
		},
		Ignored:          map[string][]string{"files": {"cwd"}},
		UnusedEnvHeaders: map[string]map[string]string{"team": {"X-Team": "TEAM_VAR"}},
		ConfigDir:        "/config",
		Sync:             SyncResult{Failed: []ServerError{{Name: "broken", Err: errors.New("disk full")}}},
		StatusErr:        errors.New("permission denied"),
	})
	requireLines(
		t,
		got,
		"Already configured in mini: linear\n",
		"Imported from your agents instead of the catalog: github\n",
		"  paused switched off in Codex\n",
		"  github in Cursor: a different config under that name is imported instead; run mini init to pick both\n",
		"files was imported without its cwd, which mini doesn't support yet; if it fails to start, edit /config/servers/files.yaml\n",
		"team was imported with its static X-Team header, since TEAM_VAR wasn't set; to use TEAM_VAR instead, set X-Team: ${TEAM_VAR} in /config/servers/team.yaml\n",
		"  templated kept in Codex: uses an environment variable in url\n",
		`  "!!!" in Cursor: its name has no letters or digits mini can use`,
		"Could not add broken: disk full\n",
		"Could not read mini's servers: permission denied\n",
	)
}

func TestSummary_connected(t *testing.T) {
	claude := agents.Agent{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"}
	codex := agents.Agent{Name: "Codex", ConfigPath: "/home/u/.codex/config.toml", RemoveDisables: true}
	cursor := agents.Agent{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"}
	gemini := agents.Agent{Name: "Gemini CLI", ConfigPath: "/home/u/.gemini/settings.json"}
	got := Summary(Report{Mini: testMini, Connected: []AgentResult{
		{
			Agent:   claude,
			Backup:  "/home/u/.claude.minibackup.json",
			Removed: []string{"github"},
			Kept: []KeptEntry{
				{Entry: "linear", Server: "linear", Err: errors.New("needs a login")},
			},
			Changed: []string{"files"},
		},
		{Agent: codex, Err: errors.New("inline table")},
		{Agent: gemini, Created: true},
		{Agent: cursor},
	}})
	requireLines(
		t,
		got,
		"Claude Code: /home/u/.claude.json backed up to /home/u/.claude.minibackup.json; to undo: cp /home/u/.claude.minibackup.json /home/u/.claude.json\n",
		"  linear stays in Claude Code: mini's linear failed its connection check: needs a login\n",
		"  files stays in Claude Code: it changed after it was checked\n",
		"Could not connect Codex: inline table\nAdd mini to /home/u/.codex/config.toml by hand:\n  [mcp_servers.mini]\n",
		"Gemini CLI: created /home/u/.gemini/settings.json; to undo: rm /home/u/.gemini/settings.json\n",
		"Restart Claude Code and Gemini CLI to start using mini.\n",
	)
}

func TestSummary_howToConnectByHand(t *testing.T) {
	list := []agents.Agent{
		{Name: "Claude Code", ConfigPath: "/home/u/.claude.json"},
		{Name: "Codex", ConfigPath: "/home/u/.codex/config.toml", RemoveDisables: true},
		{Name: "Cursor", ConfigPath: "/home/u/.cursor/mcp.json"},
	}
	mini := agents.MiniEntry{Command: "/Users/u/My Apps/mini", Args: []string{"connect"}}
	got := Summary(Report{Mini: mini, Unconnected: list})
	requireLines(t, got,
		"claude mcp add --scope user mini -- '/Users/u/My Apps/mini' connect\n",
		"[mcp_servers.mini]\n    command = \"/Users/u/My Apps/mini\"\n    args = [\"connect\"]\n",
		`"command": "/Users/u/My Apps/mini"`,
	)
	if strings.Contains(got, "Restart") {
		t.Errorf("nothing was connected, but the summary asks for a restart:\n%s", got)
	}
	requireLines(t, Summary(Report{Mini: mini}), "To connect mini to your agent, add it to its MCP config:\n")
	if got := Summary(Report{Mini: mini, HasMini: list[:1]}); strings.Contains(got, "To connect mini") {
		t.Errorf("every agent already has mini, but the summary shows how to connect one:\n%s", got)
	}
	got = Summary(Report{Mini: mini, InactiveMini: list[2:]})
	requireLines(t, got, "Cursor (/home/u/.cursor/mcp.json) has a mini entry that may not run these servers: "+
		"it's switched off, uses another config directory, or doesn't name mini by absolute path. "+
		"To use them, have it run: '/Users/u/My Apps/mini' connect\n")
	if strings.Contains(got, "To connect mini") {
		t.Errorf("the only agent has a mini entry, but the summary adds a generic step:\n%s", got)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/usr/bin/mini": "/usr/bin/mini", "a b": "'a b'", "it's": `'it'\''s'`} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
