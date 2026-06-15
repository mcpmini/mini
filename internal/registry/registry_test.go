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

func serverParams(name string, d []transport.ToolDefinition, perm *config.PermissionsConfig) registry.ServerParams {
	return registry.ServerParams{Name: name, Defs: d, Perm: perm}
}

func TestAddAndLookup(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("ci", defs("getBuild", "listBuilds"), nil))

	e, err := reg.Lookup("ci.getBuild")
	if err != nil {
		t.Fatal(err)
	}
	if e.FullName != "ci.getBuild" {
		t.Fatalf("expected ci.getBuild, got %s", e.FullName)
	}
}

func TestLookupMissing(t *testing.T) {
	reg := registry.New()
	_, err := reg.Lookup("ci.nope")
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestHiddenToolsNotIndexed(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"adminSettings"}}
	reg.AddServer(serverParams("ci", defs("getBuild", "adminSettings"), perm))

	all := reg.All()
	for _, e := range all {
		if e.Name == "ci.adminSettings" {
			t.Fatal("hidden tool should not appear in All()")
		}
	}
}

func TestProtectedPermission(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"deleteIssue"}}
	reg.AddServer(serverParams("jira", defs("getIssue", "deleteIssue"), perm))

	e, _ := reg.Lookup("jira.deleteIssue")
	if e.Permission != config.PermProtected {
		t.Fatalf("expected protected, got %s", e.Permission)
	}
}

func TestRemoveServer(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("ci", defs("getBuild"), nil))
	reg.RemoveServer("ci")

	_, err := reg.Lookup("ci.getBuild")
	if err == nil {
		t.Fatal("expected error after server removal")
	}
}

func TestSearch(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("ci", defs("getBuild", "listPipelines"), nil))

	results := reg.Search("build")
	if len(results) != 1 || results[0].Name != "ci.getBuild" {
		t.Fatalf("unexpected search results: %v", results)
	}
}

func TestAll_sortedDeterministic(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("srv", defs("zebra", "alpha", "mango"), nil))

	all := reg.All()
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
	reg := registry.New()
	reg.AddServer(serverParams("srv", defs("z_tool", "a_tool", "m_tool"), nil))

	results := reg.Search("tool")
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
	reg := registry.New()
	reg.AddServer(serverParams("alpha", defs("t1"), nil))
	reg.AddServer(serverParams("beta", defs("t2"), nil))

	names := reg.ServerNames()
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
	reg := registry.New()
	reg.AddServer(serverParams("ci", defs("a", "b", "c"), nil))

	if got := reg.ToolCount("ci"); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
	if got := reg.ToolCount("missing"); got != 0 {
		t.Errorf("expected 0 for unknown server, got %d", got)
	}
}

func TestToolCount_hiddenNotCounted(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	reg.AddServer(serverParams("ci", defs("visible", "secret"), perm))

	if got := reg.ToolCount("ci"); got != 1 {
		t.Errorf("expected 1 (hidden excluded), got %d", got)
	}
}

func TestAddAction_appearsInAll(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("fs", defs("read_file"), nil))
	reg.AddAction(config.ActionConfig{
		Name:        "read_readme",
		Description: "Read the README",
		Server:      "fs",
		Tool:        "read_file",
		DefaultArgs: map[string]any{"path": "README.md"},
	})

	all := reg.All()
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
	reg := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"dangerous_op"}}
	reg.AddServer(serverParams("srv", defs("dangerous_op"), perm))
	reg.AddAction(config.ActionConfig{
		Name:   "safe_alias",
		Server: "srv",
		Tool:   "dangerous_op",
	})

	e, err := reg.Lookup("srv.safe_alias")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("expected action to inherit protected, got %s", e.Permission)
	}
}

func TestAddAction_inheritsHiddenTargetPermission(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"secret_op"}}
	reg.AddServer(serverParams("srv", defs("secret_op"), perm))
	reg.AddAction(config.ActionConfig{
		Name:   "secret_alias",
		Server: "srv",
		Tool:   "secret_op",
	})

	if _, err := reg.Lookup("srv.secret_alias"); err == nil {
		t.Fatal("hidden action alias should not be callable through Lookup")
	}
	all := reg.All()
	for _, e := range all {
		if e.Name == "srv.secret_alias" {
			t.Fatal("hidden action alias should not appear in All()")
		}
	}
}

func TestAddAction_explicitPermissionOverrides(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Protected: []string{"dangerous_op"}}
	reg.AddServer(serverParams("srv", defs("dangerous_op"), perm))
	reg.AddAction(config.ActionConfig{
		Name:       "open_alias",
		Server:     "srv",
		Tool:       "dangerous_op",
		Permission: "open",
	})

	e, err := reg.Lookup("srv.open_alias")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermOpen {
		t.Errorf("expected open permission override, got %s", e.Permission)
	}
}

func TestAddAction_defaultArgs(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("fs", defs("read_file"), nil))
	reg.AddAction(config.ActionConfig{
		Name:        "read_readme",
		Server:      "fs",
		Tool:        "read_file",
		DefaultArgs: map[string]any{"path": "README.md"},
	})

	e, err := reg.Lookup("fs.read_readme")
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
	reg := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"AdminTool"}}
	reg.AddServer(serverParams("srv", defs("admintool"), perm))

	all := reg.All()
	for _, e := range all {
		if e.Name == "srv.admintool" {
			t.Error("case-insensitive hidden match failed: tool should be hidden")
		}
	}
}

func TestAddServer_afterRemove_noStaleEntries(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("myserver", defs("toolA", "toolB"), nil))
	reg.RemoveServer("myserver")
	reg.AddServer(serverParams("myserver", defs("toolA"), nil))

	t.Run("toolA is found", func(t *testing.T) {
		if _, err := reg.Lookup("myserver.toolA"); err != nil {
			t.Errorf("toolA should be found after re-add: %v", err)
		}
	})

	t.Run("toolB is not found", func(t *testing.T) {
		if _, err := reg.Lookup("myserver.toolB"); err == nil {
			t.Error("toolB should not exist after re-add with fewer tools")
		}
	})
}

func TestDefaultProtected_appliesToUnlistedTools(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Default: "protected"}
	reg.AddServer(serverParams("srv", defs("anyTool"), perm))

	e, err := reg.Lookup("srv.anyTool")
	if err != nil {
		t.Fatal(err)
	}
	if e.Permission != config.PermProtected {
		t.Errorf("expected protected by default, got %s", e.Permission)
	}
}

func TestAllWithHidden_includesHiddenTools(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"secretTool"}}
	reg.AddServer(serverParams("srv", defs("openTool", "secretTool"), perm))

	visible := reg.All()
	all := reg.AllWithHidden()

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
	reg := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"aaa"}}
	reg.AddServer(serverParams("srv", defs("zzz", "aaa", "mmm"), perm))

	all := reg.AllWithHidden()
	for i := 1; i < len(all); i++ {
		if all[i].Name < all[i-1].Name {
			t.Errorf("AllWithHidden not sorted: %s before %s", all[i-1].Name, all[i].Name)
		}
	}
}

func TestBuildEntry_annotationsThreaded(t *testing.T) {
	raw := json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false}`)
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: []transport.ToolDefinition{
			{Name: "get_data", Description: "desc", InputSchema: json.RawMessage(`{}`), Annotations: raw, ReadOnly: true},
		},
		Perm: nil,
	})

	e, err := reg.Lookup("svc.get_data")
	if err != nil {
		t.Fatal(err)
	}
	if string(e.Annotations) != string(raw) {
		t.Errorf("annotations not threaded through buildEntry: got %s, want %s", e.Annotations, raw)
	}
	if !e.ReadOnly {
		t.Error("readOnlyHint=true should set ReadOnly=true in entry")
	}
}
