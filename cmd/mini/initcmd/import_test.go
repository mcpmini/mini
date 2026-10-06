package initcmd

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

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

func planFor(agentList []agents.Agent, configured ...string) ImportPlan {
	var servers []config.ServerConfig
	for _, name := range configured {
		servers = append(servers, config.ServerConfig{Name: name})
	}
	return PlanImport(ImportParams{Agents: agentList, Written: servers, SelfPath: testSelf})
}

func importedNames(plan ImportPlan) []string {
	var names []string
	for _, sc := range plan.picked() {
		names = append(names, sc.Name)
	}
	return names
}

func TestPlanImport_oneServerPerConfigAndName(t *testing.T) {
	a, b := remoteEntry("https://example.com/mcp", "${A}"), remoteEntry("https://example.com/mcp", "${B}")
	tests := []struct {
		name         string
		agents       []agents.Agent
		configured   []string
		wantImported []string
		wantSkipped  []SkippedServer
	}{
		{
			name: "one config in three agents is imported once",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Codex", map[string]agents.Server{"github": a}),
				agentWith("Cursor", map[string]agents.Server{"GitHub": a}),
			},
			configured:   nil,
			wantImported: []string{"github"},
			wantSkipped:  nil,
		},
		{
			name: "same url with other credentials under a name in use is left in its agent",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Codex", map[string]agents.Server{"github": b}),
			},
			configured:   nil,
			wantImported: []string{"github"},
			wantSkipped:  []SkippedServer{{Agent: "Codex", Name: "github", Reason: SkipSecondConfig}},
		},
		{
			name: "a server switched on in any agent is imported",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": switchedOff(a)}),
				agentWith("Codex", map[string]agents.Server{"github": a}),
			},
			configured:   nil,
			wantImported: []string{"github"},
			wantSkipped:  nil,
		},
		{
			name:       "a server switched off everywhere is left in its agents",
			agents:     []agents.Agent{agentWith("Codex", map[string]agents.Server{"github": switchedOff(a)})},
			configured: nil, wantImported: nil,
			wantSkipped: []SkippedServer{{Agent: "Codex", Name: "github", Reason: SkipSwitchedOff}},
		},
		{
			name: "an enabled config claims a shared name before a switched-off one",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": switchedOff(a)}),
				agentWith("Cursor", map[string]agents.Server{"github": b}),
			},
			configured:   nil,
			wantImported: []string{"github"},
			wantSkipped:  []SkippedServer{{Agent: "Claude Code", Name: "github", Reason: SkipSwitchedOff}},
		},
		{
			name: "a second config stays out even when switched on in a later agent",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Codex", map[string]agents.Server{"github": switchedOff(b)}),
				agentWith("Cursor", map[string]agents.Server{"github": b}),
			},
			configured:   nil,
			wantImported: []string{"github"},
			wantSkipped: []SkippedServer{
				{Agent: "Codex", Name: "github", Reason: SkipSecondConfig},
				{Agent: "Cursor", Name: "github", Reason: SkipSecondConfig},
			},
		},
		{
			name:         "names are lowercased with other characters as dashes",
			agents:       []agents.Agent{agentWith("Cursor", map[string]agents.Server{"-My Server.v2!": a})},
			configured:   nil,
			wantImported: []string{"my-server-v2"},
			wantSkipped:  nil,
		},
		{
			name: "one config under two names is imported once, named by the first agent",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Cursor", map[string]agents.Server{"GitHub MCP": a}),
			},
			configured:   nil,
			wantImported: []string{"github"},
			wantSkipped:  nil,
		},
		{
			name: "a config under a name mini has is left in its agent and named",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Codex", map[string]agents.Server{"github": b}),
			},
			configured:   []string{"GitHub"},
			wantImported: nil,
			wantSkipped: []SkippedServer{
				{Agent: "Claude Code", Name: "github", Reason: SkipNameInMini},
				{Agent: "Codex", Name: "github", Reason: SkipNameInMini},
			},
		},
		{
			name: "a second config comes in under the free name another agent gives it",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Codex", map[string]agents.Server{"github": b}),
				agentWith("Cursor", map[string]agents.Server{"gh-work": b}),
			},
			configured:   nil,
			wantImported: []string{"gh-work", "github"},
			wantSkipped:  nil,
		},
		{
			name: "a config under a name mini has comes in under the free name another agent gives it",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Cursor", map[string]agents.Server{"gh": a}),
			},
			configured:   []string{"github"},
			wantImported: []string{"gh"},
			wantSkipped:  nil,
		},
		{
			name: "an unusable name goes unmentioned when its config comes in under another name",
			agents: []agents.Agent{
				agentWith("Claude Code", map[string]agents.Server{"github": a}),
				agentWith("Cursor", map[string]agents.Server{"!!!": a}),
			},
			configured:   nil,
			wantImported: []string{"github"},
			wantSkipped:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := planFor(tt.agents, tt.configured...)
			if got := importedNames(
				plan,
			); !reflect.DeepEqual(got, tt.wantImported) ||
				!reflect.DeepEqual(plan.Skipped, tt.wantSkipped) {
				t.Errorf(
					"imported = %v, skipped = %+v\nwant %v, %+v",
					got,
					plan.Skipped,
					tt.wantImported,
					tt.wantSkipped,
				)
			}
		})
	}
}

func TestPlanImport_aServerMiniHasUnderAnotherNameIsLeftOut(t *testing.T) {
	entry := remoteEntry("https://example.com/mcp", "${A}")
	configured := entry.Config
	configured.Name = "gh"
	plan := PlanImport(ImportParams{
		Agents:  []agents.Agent{agentWith("Claude Code", map[string]agents.Server{"github": entry})},
		Written: []config.ServerConfig{configured},
	})
	if len(plan.picked()) != 0 || len(plan.Skipped) != 0 {
		t.Errorf("plan = %+v, want nothing: mini already has this server as gh", plan)
	}
}

func TestPlanImport_anAgentEntryUnderAConfiguredName(t *testing.T) {
	mine := config.ServerConfig{Name: "github", Transport: "http", URL: "https://a.example.com/mcp"}
	for name, tt := range map[string]struct {
		url         string
		wantSkipped []SkippedServer
	}{
		"with mini's config is already imported": {"https://a.example.com/mcp", nil},
		"with another config is named":           {"https://b.example.com/mcp", []SkippedServer{{Agent: "Claude Code", Name: "github", Reason: SkipNameInMini}}},
	} {
		t.Run(name, func(t *testing.T) {
			plan := PlanImport(ImportParams{
				Agents: []agents.Agent{
					agentWith("Claude Code", map[string]agents.Server{"github": remoteEntry(tt.url, "")}),
				},
				Written: []config.ServerConfig{mine},
			})
			if len(plan.picked()) != 0 || !reflect.DeepEqual(plan.Skipped, tt.wantSkipped) {
				t.Errorf(
					"servers = %+v, skipped = %+v; want none imported and skipped %+v",
					plan.picked(),
					plan.Skipped,
					tt.wantSkipped,
				)
			}
		})
	}
}

func TestPlanImport_writesTheAgentConfigUnderItsNormalizedName(t *testing.T) {
	entry := remoteEntry("https://example.com/mcp", "${A}")
	plan := planFor([]agents.Agent{agentWith("Cursor", map[string]agents.Server{"My Server": entry})})
	want := config.ServerConfig{
		Name:      "my-server",
		Transport: "http",
		URL:       "https://example.com/mcp",
		Headers:   entry.Config.Headers,
	}
	if !reflect.DeepEqual(plan.picked(), []config.ServerConfig{want}) {
		t.Errorf("servers = %+v\nwant %+v", plan.picked(), want)
	}
}

func TestPlanImport_miniItselfIsNeverImported(t *testing.T) {
	servers := map[string]agents.Server{
		"mini": remoteEntry("https://example.com/mcp", ""),
		"self": {Config: config.ServerConfig{Command: testSelf, Args: []string{"connect"}}},
		"other-mini": {
			Config: config.ServerConfig{Command: "/usr/local/bin/mini", Args: []string{"--config", "/x", "connect"}},
		},
		"minify":       {Config: config.ServerConfig{Command: "minify", Args: []string{"connect"}}},
		"Mini Version": {Config: config.ServerConfig{Command: "/usr/local/bin/mini", Args: []string{"serve"}}},
	}
	plan := planFor([]agents.Agent{agentWith("Claude Code", servers)})
	if got := importedNames(plan); !reflect.DeepEqual(got, []string{"minify"}) || len(plan.Skipped) != 0 {
		t.Errorf(
			"imported = %v, skipped = %+v; want only minify, and mini's own entries left out silently",
			got,
			plan.Skipped,
		)
	}
}

func TestPlanImport_namesWhatItCantImport(t *testing.T) {
	plain := remoteEntry("https://example.com/mcp", "")
	unexpandable := plain
	unexpandable.UnexpandableRefs = []string{"an environment variable in url"}
	agentList := []agents.Agent{
		agentWith("Codex", map[string]agents.Server{"templated": unexpandable, "!!!": plain}),
		{
			Name:       "Broken",
			ConfigPath: "x",
			Read:       func(string) (map[string]agents.Server, error) { return nil, errors.New("invalid JSON") },
		},
	}
	plan := planFor(agentList)
	want := []SkippedServer{
		{
			Agent:  "Codex",
			Name:   "templated",
			Reason: SkipUnexpandableRefs,
			Refs:   []string{"an environment variable in url"},
		},
		{Agent: "Codex", Name: "!!!", Reason: SkipEmptyName},
	}
	if len(plan.picked()) != 0 || !reflect.DeepEqual(plan.Skipped, want) {
		t.Errorf("servers = %+v\nskipped = %+v\nwant none and %+v", plan.picked(), plan.Skipped, want)
	}
	if len(plan.Unreadable) != 1 || plan.Unreadable[0].Agent != "Broken" || plan.Unreadable[0].Err == nil {
		t.Errorf("unreadable = %+v, want Broken with its error", plan.Unreadable)
	}
}

func TestPlanImport_caveatsFromEveryAgentLandOnTheImportedServer(t *testing.T) {
	files := remoteEntry("https://files.example/mcp", "")
	files.IgnoredRunSettings = []string{"cwd"}
	files.UnusedEnvHeaders = map[string]string{"X-Team": "TEAM_VAR"}
	envFile := remoteEntry("https://files.example/mcp", "")
	envFile.IgnoredRunSettings = []string{"envFile", "cwd"}
	other := remoteEntry("https://other.example/mcp", "")
	other.IgnoredRunSettings = []string{"cwd"}
	plan := planFor([]agents.Agent{
		agentWith("Codex", map[string]agents.Server{"files": files}),
		agentWith("Cursor", map[string]agents.Server{"files": envFile}),
		agentWith("Windsurf", map[string]agents.Server{"files": switchedOff(other)}),
	})
	want := map[string][]string{"files": {"cwd", "envFile"}, "files-2": {"cwd"}}
	if !reflect.DeepEqual(plan.DroppedSettings, want) {
		t.Errorf("ignored = %v, want %v: the offered Windsurf config keeps its own notes", plan.DroppedSettings, want)
	}
	if want := map[string]map[string]string{
		"files": {"X-Team": "TEAM_VAR"},
	}; !reflect.DeepEqual(
		plan.StaticHeaders,
		want,
	) {
		t.Errorf("unused env headers = %v, want %v", plan.StaticHeaders, want)
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

func TestPlanImport_offersWhatItLeavesOutUnpicked(t *testing.T) {
	a, b := remoteEntry("https://example.com/mcp", "${A}"), remoteEntry("https://example.com/mcp", "${B}")
	plan := planFor([]agents.Agent{
		agentWith(
			"Claude Code",
			map[string]agents.Server{"github": a, "linear": remoteEntry("https://linear.example/mcp", "")},
		),
		agentWith(
			"Codex",
			map[string]agents.Server{"github": b, "notes": switchedOff(remoteEntry("https://notes.example/mcp", ""))},
		),
	}, "linear", "github-2")
	type row struct {
		name   string
		from   []AgentEntry
		picked bool
		reason SkipReason
		shares string
	}
	var got []row
	for _, c := range plan.Candidates {
		got = append(
			got,
			row{name: c.Server.Name, from: c.From, picked: c.Picked, reason: c.Reason, shares: c.SharesName},
		)
	}
	want := []row{
		{name: "github", from: []AgentEntry{{"Claude Code", "github"}}, picked: true, reason: SkipNone, shares: ""},
		{
			name:   "github-3",
			from:   []AgentEntry{{"Codex", "github"}},
			picked: false,
			reason: SkipSecondConfig,
			shares: "github",
		},
		{name: "notes", from: []AgentEntry{{"Codex", "notes"}}, picked: false, reason: SkipSwitchedOff, shares: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf(
			"candidates = %+v\nwant %+v: a second config is offered under the next free suffix, a switched-off one "+
				"under its name, and a name mini has isn't offered",
			got,
			want,
		)
	}
}

func TestPlanImport_aSecondConfigSharesTheNameItsSuffixComesFrom(t *testing.T) {
	x, y := remoteEntry("https://gh.example.com/mcp", "${X}"), remoteEntry("https://gh.example.com/mcp", "${Y}")
	plan := planFor([]agents.Agent{
		agentWith("Claude Code", map[string]agents.Server{"gh": y}),
		agentWith("Codex", map[string]agents.Server{"github": x}),
		agentWith("Cursor", map[string]agents.Server{"gh": x}),
	}, "github")
	i := slices.IndexFunc(plan.Candidates, func(c Candidate) bool { return c.Server.Name == "gh-2" })
	if i < 0 || plan.Candidates[i].SharesName != "gh" {
		t.Errorf("candidates = %+v; want gh-2 sharing gh, not the github mini already has", plan.Candidates)
	}
}
