package agents

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var contractMini = MiniEntry{Command: "/opt/mini/bin/mini", Args: []string{"connect"}}

func configWith(t *testing.T, agent Agent, servers map[string]string) []byte {
	t.Helper()
	switch agent.Name {
	case "Codex":
		return codexConfigWith(servers)
	case "Claude Code", "Claude Desktop", "Cursor", "Windsurf":
		return jsonConfigWith(t, servers)
	}
	t.Fatalf("no config format for agent %q", agent.Name)
	return nil
}

func codexConfigWith(servers map[string]string) []byte {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		fmt.Fprintf(&b, "[mcp_servers.%s]\ncommand = %q\n", name, servers[name])
	}
	return []byte(b.String())
}

func jsonConfigWith(t *testing.T, servers map[string]string) []byte {
	t.Helper()
	entries := map[string]any{}
	for name, command := range servers {
		entries[name] = map[string]any{"command": command}
	}
	data, err := json.Marshal(map[string]any{"mcpServers": entries})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return data
}

func connectAndParse(t *testing.T, agent Agent, cfg []byte, remove []string, mini *MiniEntry) map[string]Server {
	t.Helper()
	out, err := agent.Connect(cfg, remove, mini)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	got, err := agent.Parse(out)
	if err != nil {
		t.Fatalf("Parse after Connect: %v\n%s", err, out)
	}
	return got
}

func mustParse(t *testing.T, agent Agent, cfg []byte) map[string]Server {
	t.Helper()
	got, err := agent.Parse(cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return got
}

func TestKnownAgents_followTheAgentContract(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	for _, agent := range Known(tempDir(t)) {
		if agent.ConfigPath == "" {
			continue
		}
		t.Run(agent.Name, func(t *testing.T) {
			t.Run(
				"connect into an empty config adds a mini that runs",
				func(t *testing.T) { connectAddsRunnableMini(t, agent) },
			)
			t.Run("connect removes only the named entries", func(t *testing.T) { connectRemovesOnlyNamed(t, agent) })
			t.Run("the user's own mini entry is never changed", func(t *testing.T) { connectKeepsUsersMini(t, agent) })
			t.Run("a missing file is an error", func(t *testing.T) { missingFileIsAnError(t, agent) })
			t.Run("no servers is an empty result", func(t *testing.T) { noServersIsEmpty(t, agent) })
			t.Run("a malformed config is an error", func(t *testing.T) { malformedConfigIsAnError(t, agent) })
		})
	}
}

func connectAddsRunnableMini(t *testing.T, agent Agent) {
	mini := contractMini
	got := connectAndParse(t, agent, nil, nil, &mini)[MiniKey]
	if got.Config.Command != mini.Command || !reflect.DeepEqual(got.Config.Args, mini.Args) || got.Disabled {
		t.Errorf("mini entry = %#v, want command %q args %v enabled", got, mini.Command, mini.Args)
	}
}

func connectRemovesOnlyNamed(t *testing.T, agent Agent) {
	mini := contractMini
	cfg := configWith(t, agent, map[string]string{"files": "files-server", "notes": "notes-server"})
	notesBefore := mustParse(t, agent, cfg)["notes"]
	got := connectAndParse(t, agent, cfg, []string{"files"}, &mini)
	if !reflect.DeepEqual(got["notes"], notesBefore) {
		t.Errorf("notes = %#v, want it unchanged from %#v", got["notes"], notesBefore)
	}
	files, present := got["files"]
	if switchedOff := present && files.Disabled; switchedOff != agent.RemoveDisables || (present && !switchedOff) {
		t.Errorf("files = %#v, present %v; want it switched off if RemoveDisables (%v), else gone",
			files, present, agent.RemoveDisables)
	}
	if m, present := got[MiniKey]; !present || m.Disabled {
		t.Errorf("mini = %#v (present %v), want present and enabled", m, present)
	}
}

func connectKeepsUsersMini(t *testing.T, agent Agent) {
	mini := contractMini
	cfg := configWith(t, agent, map[string]string{MiniKey: "custom-mini"})
	before := mustParse(t, agent, cfg)[MiniKey]
	got := connectAndParse(t, agent, cfg, []string{MiniKey}, &mini)[MiniKey]
	if !reflect.DeepEqual(got, before) {
		t.Errorf("mini = %#v, want the user's entry %#v", got, before)
	}
}

func missingFileIsAnError(t *testing.T, agent Agent) {
	if _, err := agent.Read(filepath.Join(tempDir(t), "missing")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func noServersIsEmpty(t *testing.T, agent Agent) {
	got, err := agent.Parse(configWith(t, agent, nil))
	if err != nil || len(got) != 0 {
		t.Fatalf("Parse(empty) = %v, %v; want nothing and no error", got, err)
	}
}

func malformedConfigIsAnError(t *testing.T, agent Agent) {
	if _, err := agent.Parse([]byte("{not valid")); err == nil {
		t.Fatal("expected a parse error")
	}
}
