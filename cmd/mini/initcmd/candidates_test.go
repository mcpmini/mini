package initcmd

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

const testSelf = "/opt/mini/bin/mini-under-test"

func agentWith(name string, servers map[string]agents.Server) agents.Agent {
	return agents.Agent{Name: name, ConfigPath: name, Read: func(string) (map[string]agents.Server, error) {
		return servers, nil
	}}
}

func remoteEntry(url, token string) agents.Server {
	sc := config.ServerConfig{Transport: "http", URL: url}
	if token != "" {
		sc.Headers = map[string]string{"Authorization": "Bearer " + token}
	}
	return agents.Server{Config: sc}
}

func switchedOff(s agents.Server) agents.Server {
	s.Disabled = true
	return s
}

type row struct {
	name    string
	agents  []string
	checked bool
}

func rows(candidates []Candidate) []row {
	var out []row
	for _, c := range candidates {
		r := row{name: c.Name, checked: c.Checked}
		for _, source := range c.Sources {
			r.agents = append(r.agents, source.Agent)
		}
		out = append(out, r)
	}
	return out
}

func find(agentList []agents.Agent, configured ...string) ([]Candidate, []SkippedServer) {
	var servers []config.ServerConfig
	for _, name := range configured {
		servers = append(servers, config.ServerConfig{Name: name})
	}
	return FindServers(FindParams{Agents: agentList, Configured: servers, SelfPath: testSelf})
}

func TestFindServers_merging(t *testing.T) {
	a, b := remoteEntry("https://example.com/mcp", "${A}"), remoteEntry("https://example.com/mcp", "${B}")
	tests := []struct {
		name       string
		agents     []agents.Agent
		configured []string
		want       []row
	}{
		{"one config in three agents is one row",
			[]agents.Agent{agentWith("Claude Code", map[string]agents.Server{"github": a}), agentWith("Codex", map[string]agents.Server{"github": a}), agentWith("Cursor", map[string]agents.Server{"GitHub": a})},
			nil, []row{{"github", []string{"Claude Code", "Codex", "Cursor"}, true}}},
		{"same url with other credentials is a second, unticked row",
			[]agents.Agent{agentWith("Claude Code", map[string]agents.Server{"github": a}), agentWith("Codex", map[string]agents.Server{"github": b})},
			nil, []row{{"github", []string{"Claude Code"}, true}, {"github-2", []string{"Codex"}, false}}},
		{"suffixes skip configured names and names other rows use",
			[]agents.Agent{agentWith("Claude Code", map[string]agents.Server{"github": a, "github-3": remoteEntry("https://other.example/mcp", "")}), agentWith("Codex", map[string]agents.Server{"github": b})},
			[]string{"github-2"}, []row{{"github", []string{"Claude Code"}, true}, {"github-3", []string{"Claude Code"}, true}, {"github-4", []string{"Codex"}, false}}},
		{"a row is ticked when any agent has it switched on",
			[]agents.Agent{agentWith("Claude Code", map[string]agents.Server{"github": switchedOff(a)}), agentWith("Codex", map[string]agents.Server{"github": a})},
			nil, []row{{"github", []string{"Claude Code", "Codex"}, true}}},
		{"a row switched off everywhere starts unticked",
			[]agents.Agent{agentWith("Codex", map[string]agents.Server{"github": switchedOff(a)})},
			nil, []row{{"github", []string{"Codex"}, false}}},
		{"a second config stays unticked even when switched on in a later agent",
			[]agents.Agent{agentWith("Claude Code", map[string]agents.Server{"github": a}), agentWith("Codex", map[string]agents.Server{"github": switchedOff(b)}), agentWith("Cursor", map[string]agents.Server{"github": b})},
			nil, []row{{"github", []string{"Claude Code"}, true}, {"github-2", []string{"Codex", "Cursor"}, false}}},
		{"names are lowercased with other characters as dashes",
			[]agents.Agent{agentWith("Cursor", map[string]agents.Server{"-My Server.v2!": a})},
			nil, []row{{"my-server-v2", []string{"Cursor"}, true}}},
		{"a configured name is hidden whatever its config",
			[]agents.Agent{agentWith("Claude Code", map[string]agents.Server{"github": a}), agentWith("Codex", map[string]agents.Server{"github": b})},
			[]string{"GitHub"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates, _ := find(tt.agents, tt.configured...)
			if got := rows(candidates); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rows = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestFindServers_rowCarriesItsSources(t *testing.T) {
	entry := remoteEntry("https://example.com/mcp", "${A}")
	candidates, _ := find([]agents.Agent{agentWith("Cursor", map[string]agents.Server{"My Server": entry})})
	want := Candidate{
		Name:    "my-server",
		Config:  config.ServerConfig{Name: "my-server", Transport: "http", URL: "https://example.com/mcp", Headers: entry.Config.Headers},
		Sources: []Source{{Agent: "Cursor", Name: "My Server", Entry: entry.Config}},
		Checked: true,
	}
	if !reflect.DeepEqual(candidates, []Candidate{want}) {
		t.Errorf("candidates = %+v\nwant %+v", candidates, want)
	}
}

func TestFindServers_miniItselfIsNeverACandidate(t *testing.T) {
	servers := map[string]agents.Server{
		"mini":         remoteEntry("https://example.com/mcp", ""),
		"self":         {Config: config.ServerConfig{Command: testSelf, Args: []string{"connect"}}},
		"other-mini":   {Config: config.ServerConfig{Command: "/usr/local/bin/mini", Args: []string{"--config", "/x", "connect"}}},
		"minify":       {Config: config.ServerConfig{Command: "minify", Args: []string{"connect"}}},
		"Mini Version": {Config: config.ServerConfig{Command: "/usr/local/bin/mini", Args: []string{"serve"}}},
	}
	candidates, skipped := find([]agents.Agent{agentWith("Claude Code", servers)})
	if got := rows(candidates); !reflect.DeepEqual(got, []row{{"minify", []string{"Claude Code"}, true}}) {
		t.Errorf("rows = %+v, want only minify", got)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %+v, want mini's own entries left out silently", skipped)
	}
}

func TestFindServers_skipped(t *testing.T) {
	plain := remoteEntry("https://example.com/mcp", "")
	limited, approval, cwd := plain, plain, plain
	limited.LimitsTools, approval.RequiresApproval, cwd.Unsupported = true, true, []string{"cwd"}
	agentList := []agents.Agent{
		agentWith("Codex", map[string]agents.Server{"filtered": limited, "approved": approval, "files": cwd, "!!!": plain}),
		{Name: "Broken", ConfigPath: "x", Read: func(string) (map[string]agents.Server, error) { return nil, errors.New("invalid JSON") }},
	}
	candidates, skipped := find(agentList)
	want := []SkippedServer{
		{Agent: "Codex", Name: "!!!", Reason: SkipEmptyName},
		{Agent: "Codex", Name: "approved", Reason: SkipRequiresApproval},
		{Agent: "Codex", Name: "files", Reason: SkipUnsupported, Settings: []string{"cwd"}},
		{Agent: "Codex", Name: "filtered", Reason: SkipLimitsTools},
	}
	if len(candidates) != 0 || !reflect.DeepEqual(skipped, want) {
		t.Errorf("candidates = %+v\nskipped = %+v\nwant no candidates and %+v", candidates, skipped, want)
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"github": "github", "GitHub": "github", "my_server": "my_server", "a  b..c": "a-b-c",
		"--x--": "x", "Café": "caf", "!!!": "", "": "",
	} {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
