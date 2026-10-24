package registry_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/registry"
)

func TestAlias_basicResolution(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "github",
		Defs: defs("list_pull_requests", "get_issue"),
		AliasByToolName: map[string]string{
			"list_pull_requests": "list_prs",
		},
	})

	t.Run("alias appears in list, real name does not", func(t *testing.T) {
		all := reg.AllFull()
		names := map[string]bool{}
		for _, e := range all {
			names[e.FullName] = true
		}
		if names["github.list_pull_requests"] {
			t.Error("real tool name should not appear in list when aliased")
		}
		if !names["github.list_prs"] {
			t.Error("alias should appear in list")
		}
		if !names["github.get_issue"] {
			t.Error("non-aliased tool should still appear")
		}
	})

	t.Run("lookup by alias resolves to real tool", func(t *testing.T) {
		e, err := reg.Lookup("github.list_prs")
		if err != nil {
			t.Fatalf("lookup by alias failed: %v", err)
		}
		if e.ToolName.UpstreamName != "list_pull_requests" {
			t.Errorf("expected ToolName.UpstreamName=list_pull_requests, got %q", e.ToolName.UpstreamName)
		}
		if e.ToolName.Name() != "list_prs" {
			t.Errorf("expected ToolName.Name()=list_prs, got %q", e.ToolName.Name())
		}
	})

	t.Run("real name not lookupable", func(t *testing.T) {
		if _, err := reg.Lookup("github.list_pull_requests"); err == nil {
			t.Error("real tool name should not be lookupable when aliased")
		}
	})
}

func TestAlias_invalidAliasIgnored(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("my_tool"),
		AliasByToolName: map[string]string{
			"my_tool": "bad alias!", // spaces and ! are invalid
		},
	})

	e, err := reg.Lookup("svc.my_tool")
	if err != nil {
		t.Fatalf("tool should be reachable under real name when alias is invalid: %v", err)
	}
	if e.ToolName.Alias != "" {
		t.Errorf("invalid alias should result in no alias, got %q", e.ToolName.Alias)
	}
}

func TestAlias_collisionWithRealToolName(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("toolA", "toolB"),
		AliasByToolName: map[string]string{
			"toolA": "toolB",
		},
	})

	t.Run("both tools reachable under real names", func(t *testing.T) {
		if _, err := reg.Lookup("svc.toolA"); err != nil {
			t.Errorf("toolA should be reachable under real name on collision: %v", err)
		}
		if _, err := reg.Lookup("svc.toolB"); err != nil {
			t.Errorf("toolB should be reachable under real name: %v", err)
		}
	})

	t.Run("tool count is correct", func(t *testing.T) {
		if got := reg.ToolCount("svc"); got != 2 {
			t.Errorf("expected 2 tools (collision drops alias), got %d", got)
		}
	})
}

func TestAlias_collisionWithAnotherAlias(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("toolA", "toolB"),
		AliasByToolName: map[string]string{
			"toolA": "shared",
			"toolB": "shared",
		},
	})

	t.Run("both tools reachable under real names", func(t *testing.T) {
		if _, err := reg.Lookup("svc.toolA"); err != nil {
			t.Errorf("toolA should be reachable under real name on alias collision: %v", err)
		}
		if _, err := reg.Lookup("svc.toolB"); err != nil {
			t.Errorf("toolB should be reachable under real name on alias collision: %v", err)
		}
	})

	t.Run("alias name is not claimed by either tool", func(t *testing.T) {
		if _, err := reg.Lookup("svc.shared"); err == nil {
			t.Error("colliding alias should not be reachable")
		}
	})

	t.Run("tool count is unchanged", func(t *testing.T) {
		if got := reg.ToolCount("svc"); got != 2 {
			t.Errorf("expected 2 tools, got %d", got)
		}
	})
}

func TestAlias_reconnectYieldsToNewRealTool(t *testing.T) {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "gh",
		Defs: defs("list_pull_requests"),
		AliasByToolName: map[string]string{
			"list_pull_requests": "list_prs",
		},
	})

	if _, err := reg.Lookup("gh.list_prs"); err != nil {
		t.Fatalf("alias should be reachable before reconnect: %v", err)
	}

	reg.ReplaceServer(registry.ServerParams{
		Name: "gh",
		Defs: defs("list_pull_requests", "list_prs"),
		AliasByToolName: map[string]string{
			"list_pull_requests": "list_prs",
		},
	})

	t.Run("both tools reachable under real names", func(t *testing.T) {
		if _, err := reg.Lookup("gh.list_pull_requests"); err != nil {
			t.Errorf("list_pull_requests should be reachable: %v", err)
		}
		e, err := reg.Lookup("gh.list_prs")
		if err != nil {
			t.Fatalf("list_prs should be reachable as a real tool: %v", err)
		}
		if e.ToolName.Alias != "" {
			t.Errorf("list_prs should route to itself, not an alias, got Alias=%q", e.ToolName.Alias)
		}
	})

	t.Run("tool count reflects both real tools", func(t *testing.T) {
		if got := reg.ToolCount("gh"); got != 2 {
			t.Errorf("expected 2 tools, got %d", got)
		}
	})
}
