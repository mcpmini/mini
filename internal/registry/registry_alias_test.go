package registry_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
)

func TestAlias_basicResolution(t *testing.T) {
	r := registry.New()
	r.AddServer(registry.ServerParams{
		Name: "github",
		Defs: defs("list_pull_requests", "get_issue"),
		Aliases: map[string]string{
			"list_pull_requests": "list_prs",
		},
	})

	t.Run("alias appears in list, real name does not", func(t *testing.T) {
		all := r.All()
		names := map[string]bool{}
		for _, e := range all {
			names[e.Name] = true
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
		e, err := r.Lookup("github.list_prs")
		if err != nil {
			t.Fatalf("lookup by alias failed: %v", err)
		}
		if e.UpstreamTool != "list_pull_requests" {
			t.Errorf("expected UpstreamTool=list_pull_requests, got %q", e.UpstreamTool)
		}
		if e.Name != "list_prs" {
			t.Errorf("expected Name=list_prs, got %q", e.Name)
		}
	})

	t.Run("real name not lookupable", func(t *testing.T) {
		if _, err := r.Lookup("github.list_pull_requests"); err == nil {
			t.Error("real tool name should not be lookupable when aliased")
		}
	})

	t.Run("search finds alias", func(t *testing.T) {
		results := r.Search("list_prs")
		if len(results) != 1 || results[0].Name != "github.list_prs" {
			t.Errorf("search should find alias, got: %v", results)
		}
	})
}

func TestAlias_invalidAliasIgnored(t *testing.T) {
	r := registry.New()
	r.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("my_tool"),
		Aliases: map[string]string{
			"my_tool": "bad alias!", // spaces and ! are invalid
		},
	})

	e, err := r.Lookup("svc.my_tool")
	if err != nil {
		t.Fatalf("tool should be reachable under real name when alias is invalid: %v", err)
	}
	if e.UpstreamTool != "" {
		t.Errorf("invalid alias should result in no UpstreamTool, got %q", e.UpstreamTool)
	}
}

func TestAlias_collisionWithRealToolName(t *testing.T) {
	r := registry.New()
	r.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("toolA", "toolB"),
		Aliases: map[string]string{
			"toolA": "toolB",
		},
	})

	t.Run("both tools reachable under real names", func(t *testing.T) {
		if _, err := r.Lookup("svc.toolA"); err != nil {
			t.Errorf("toolA should be reachable under real name on collision: %v", err)
		}
		if _, err := r.Lookup("svc.toolB"); err != nil {
			t.Errorf("toolB should be reachable under real name: %v", err)
		}
	})

	t.Run("tool count is correct", func(t *testing.T) {
		if got := r.ToolCount("svc"); got != 2 {
			t.Errorf("expected 2 tools (collision drops alias), got %d", got)
		}
	})
}

func TestAlias_collisionWithAnotherAlias(t *testing.T) {
	r := registry.New()
	r.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("toolA", "toolB"),
		Aliases: map[string]string{
			"toolA": "shared",
			"toolB": "shared",
		},
	})

	t.Run("both tools reachable under real names", func(t *testing.T) {
		if _, err := r.Lookup("svc.toolA"); err != nil {
			t.Errorf("toolA should be reachable under real name on alias collision: %v", err)
		}
		if _, err := r.Lookup("svc.toolB"); err != nil {
			t.Errorf("toolB should be reachable under real name on alias collision: %v", err)
		}
	})

	t.Run("alias name is not claimed by either tool", func(t *testing.T) {
		if _, err := r.Lookup("svc.shared"); err == nil {
			t.Error("colliding alias should not be reachable")
		}
	})

	t.Run("tool count is unchanged", func(t *testing.T) {
		if got := r.ToolCount("svc"); got != 2 {
			t.Errorf("expected 2 tools, got %d", got)
		}
	})
}

func TestAlias_actionTargetingAliasByAliasName(t *testing.T) {
	r := registry.New()
	r.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("real_tool"),
		Aliases: map[string]string{
			"real_tool": "aliased_tool",
		},
	})
	r.AddAction(config.ActionConfig{
		Name:   "my_action",
		Server: "svc",
		Tool:   "aliased_tool",
	})

	e, err := r.Lookup("svc.my_action")
	if err != nil {
		t.Fatalf("action lookup failed: %v", err)
	}
	if e.TargetTool != "real_tool" {
		t.Errorf("action should resolve alias to real_tool, got %q", e.TargetTool)
	}
}

func TestAlias_actionTargetingAliasByRealName_inheritsPermission(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"real_tool"}}
	r.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: defs("real_tool"),
		Perm: perm,
		Aliases: map[string]string{
			"real_tool": "aliased_tool",
		},
	})
	r.AddAction(config.ActionConfig{
		Name:   "my_action",
		Server: "svc",
		Tool:   "real_tool",
	})

	e, err := r.Lookup("svc.my_action")
	if err != nil {
		t.Fatalf("action lookup failed: %v", err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("action should inherit protected permission from aliased real_tool, got %s", e.Permission)
	}
}

func TestAlias_reconnectYieldsToNewRealTool(t *testing.T) {
	r := registry.New()
	r.AddServer(registry.ServerParams{
		Name: "gh",
		Defs: defs("list_pull_requests"),
		Aliases: map[string]string{
			"list_pull_requests": "list_prs",
		},
	})

	if _, err := r.Lookup("gh.list_prs"); err != nil {
		t.Fatalf("alias should be reachable before reconnect: %v", err)
	}

	r.ReplaceServer(registry.ServerParams{
		Name: "gh",
		Defs: defs("list_pull_requests", "list_prs"),
		Aliases: map[string]string{
			"list_pull_requests": "list_prs",
		},
	})

	t.Run("both tools reachable under real names", func(t *testing.T) {
		if _, err := r.Lookup("gh.list_pull_requests"); err != nil {
			t.Errorf("list_pull_requests should be reachable: %v", err)
		}
		e, err := r.Lookup("gh.list_prs")
		if err != nil {
			t.Fatalf("list_prs should be reachable as a real tool: %v", err)
		}
		if e.UpstreamTool != "" {
			t.Errorf("list_prs should route to itself, not an alias, got UpstreamTool=%q", e.UpstreamTool)
		}
	})

	t.Run("tool count reflects both real tools", func(t *testing.T) {
		if got := r.ToolCount("gh"); got != 2 {
			t.Errorf("expected 2 tools, got %d", got)
		}
	})
}
