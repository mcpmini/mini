package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

func defs(names ...string) []transport.ToolDefinition {
	out := make([]transport.ToolDefinition, len(names))
	for i, n := range names {
		out[i] = transport.ToolDefinition{Name: n, Description: "desc for " + n, InputSchema: json.RawMessage(`{}`)}
	}
	return out
}

func TestAddAndLookup(t *testing.T) {
	r := registry.New()
	r.AddServer("ci", defs("getBuild", "listBuilds"), nil)

	e, err := r.Lookup("ci.getBuild")
	if err != nil {
		t.Fatal(err)
	}
	if e.FullName != "ci.getBuild" {
		t.Fatalf("expected ci.getBuild, got %s", e.FullName)
	}
}

func TestLookupMissing(t *testing.T) {
	r := registry.New()
	_, err := r.Lookup("ci.nope")
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestHiddenToolsNotIndexed(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"adminSettings"}}
	r.AddServer("ci", defs("getBuild", "adminSettings"), perm)

	all := r.All()
	for _, e := range all {
		if e.Name == "ci.adminSettings" {
			t.Fatal("hidden tool should not appear in All()")
		}
	}
}

func TestProtectedPermission(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"deleteIssue"}}
	r.AddServer("jira", defs("getIssue", "deleteIssue"), perm)

	e, _ := r.Lookup("jira.deleteIssue")
	if e.Permission != config.PermProtected {
		t.Fatalf("expected protected, got %s", e.Permission)
	}
}

func TestRemoveServer(t *testing.T) {
	r := registry.New()
	r.AddServer("ci", defs("getBuild"), nil)
	r.RemoveServer("ci")

	_, err := r.Lookup("ci.getBuild")
	if err == nil {
		t.Fatal("expected error after server removal")
	}
}

func TestSearch(t *testing.T) {
	r := registry.New()
	r.AddServer("ci", defs("getBuild", "listPipelines"), nil)

	results := r.Search("build")
	if len(results) != 1 || results[0].Name != "ci.getBuild" {
		t.Fatalf("unexpected search results: %v", results)
	}
}

func TestAll_sortedDeterministic(t *testing.T) {
	r := registry.New()
	r.AddServer("srv", defs("zebra", "alpha", "mango"), nil)

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Name < all[i-1].Name {
			t.Errorf("All() not sorted: %s before %s", all[i-1].Name, all[i].Name)
		}
	}
}

func TestSearch_sortedDeterministic(t *testing.T) {
	r := registry.New()
	r.AddServer("srv", defs("z_tool", "a_tool", "m_tool"), nil)

	results := r.Search("tool")
	if len(results) != 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	for i := 1; i < len(results); i++ {
		if results[i].Name < results[i-1].Name {
			t.Errorf("Search() not sorted: %s before %s", results[i-1].Name, results[i].Name)
		}
	}
}

func TestServerNames(t *testing.T) {
	r := registry.New()
	r.AddServer("alpha", defs("t1"), nil)
	r.AddServer("beta", defs("t2"), nil)

	names := r.ServerNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 server names, got %d: %v", len(names), names)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Errorf("missing expected server names, got: %v", names)
	}
}

func TestToolCount(t *testing.T) {
	r := registry.New()
	r.AddServer("ci", defs("a", "b", "c"), nil)

	if got := r.ToolCount("ci"); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
	if got := r.ToolCount("missing"); got != 0 {
		t.Errorf("expected 0 for unknown server, got %d", got)
	}
}

func TestToolCount_hiddenNotCounted(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	r.AddServer("ci", defs("visible", "secret"), perm)

	if got := r.ToolCount("ci"); got != 1 {
		t.Errorf("expected 1 (hidden excluded), got %d", got)
	}
}

func TestAddAction_appearsInAll(t *testing.T) {
	r := registry.New()
	r.AddServer("fs", defs("read_file"), nil)
	r.AddAction(config.ActionConfig{
		Name:        "read_readme",
		Description: "Read the README",
		Server:      "fs",
		Tool:        "read_file",
		DefaultArgs: map[string]any{"path": "README.md"},
	})

	all := r.All()
	found := false
	for _, e := range all {
		if e.Name == "fs.read_readme" {
			found = true
		}
	}
	if !found {
		t.Error("action did not appear in All()")
	}
}

func TestAddAction_inheritsTargetPermission(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"dangerous_op"}}
	r.AddServer("srv", defs("dangerous_op"), perm)
	r.AddAction(config.ActionConfig{
		Name:   "safe_alias",
		Server: "srv",
		Tool:   "dangerous_op",
	})

	e, err := r.Lookup("srv.safe_alias")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("expected action to inherit protected, got %s", e.Permission)
	}
}

func TestAddAction_explicitPermissionOverrides(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"dangerous_op"}}
	r.AddServer("srv", defs("dangerous_op"), perm)
	r.AddAction(config.ActionConfig{
		Name:       "open_alias",
		Server:     "srv",
		Tool:       "dangerous_op",
		Permission: "open",
	})

	e, err := r.Lookup("srv.open_alias")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermOpen {
		t.Errorf("expected open permission override, got %s", e.Permission)
	}
}

func TestAddAction_defaultArgs(t *testing.T) {
	r := registry.New()
	r.AddServer("fs", defs("read_file"), nil)
	r.AddAction(config.ActionConfig{
		Name:        "read_readme",
		Server:      "fs",
		Tool:        "read_file",
		DefaultArgs: map[string]any{"path": "README.md"},
	})

	e, err := r.Lookup("fs.read_readme")
	if err != nil {
		t.Fatal(err)
	}
	if e.TargetTool != "read_file" {
		t.Errorf("expected TargetTool=read_file, got %s", e.TargetTool)
	}
	if e.DefaultArgs["path"] != "README.md" {
		t.Errorf("expected default path=README.md, got %v", e.DefaultArgs["path"])
	}
}

func TestResolvePermission_caseInsensitive(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"AdminTool"}}
	// Upstream sends "admintool" (different case) — should still be hidden.
	r.AddServer("srv", defs("admintool"), perm)

	all := r.All()
	for _, e := range all {
		if e.Name == "srv.admintool" {
			t.Error("case-insensitive hidden match failed: tool should be hidden")
		}
	}
}

func TestAddServer_afterRemove_noStaleEntries(t *testing.T) {
	r := registry.New()
	r.AddServer("myserver", defs("toolA", "toolB"), nil)
	r.RemoveServer("myserver")
	r.AddServer("myserver", defs("toolA"), nil)

	t.Run("toolA is found", func(t *testing.T) {
		if _, err := r.Lookup("myserver.toolA"); err != nil {
			t.Errorf("toolA should be found after re-add: %v", err)
		}
	})

	t.Run("toolB is not found", func(t *testing.T) {
		if _, err := r.Lookup("myserver.toolB"); err == nil {
			t.Error("toolB should not exist after re-add with fewer tools")
		}
	})
}

func TestDefaultProtected_appliesToUnlistedTools(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Default: "protected"}
	r.AddServer("srv", defs("anyTool"), perm)

	e, err := r.Lookup("srv.anyTool")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("expected protected by default, got %s", e.Permission)
	}
}

func TestAllWithHidden_includesHiddenTools(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"secretTool"}}
	r.AddServer("srv", defs("openTool", "secretTool"), perm)

	visible := r.All()
	all := r.AllWithHidden()

	if len(visible) != 1 {
		t.Errorf("expected 1 visible tool, got %d", len(visible))
	}
	if len(all) != 2 {
		t.Errorf("expected 2 tools (hidden included), got %d", len(all))
	}

	names := map[string]bool{}
	for _, e := range all {
		names[e.Name] = true
	}
	if !names["srv.openTool"] || !names["srv.secretTool"] {
		t.Errorf("AllWithHidden missing expected tools, got: %v", all)
	}
}

func TestAllWithHidden_sorted(t *testing.T) {
	r := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"aaa"}}
	r.AddServer("srv", defs("zzz", "aaa", "mmm"), perm)

	all := r.AllWithHidden()
	for i := 1; i < len(all); i++ {
		if all[i].Name < all[i-1].Name {
			t.Errorf("AllWithHidden not sorted: %s before %s", all[i-1].Name, all[i].Name)
		}
	}
}
