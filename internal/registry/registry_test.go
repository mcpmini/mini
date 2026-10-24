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

	all := reg.AllFull()
	for _, e := range all {
		if e.FullName == "ci.adminSettings" {
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

func TestAll_sortedDeterministic(t *testing.T) {
	reg := registry.New()
	reg.AddServer(serverParams("srv", defs("zebra", "alpha", "mango"), nil))

	all := reg.AllFull()
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].FullName < all[i-1].FullName {
			t.Errorf("All() not sorted: %s before %s", all[i-1].FullName, all[i].FullName)
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

func TestResolvePermission_caseInsensitive(t *testing.T) {
	reg := registry.New()
	perm := &config.PermissionsConfig{Hidden: []string{"AdminTool"}}
	reg.AddServer(serverParams("srv", defs("admintool"), perm))

	all := reg.AllFull()
	for _, e := range all {
		if e.FullName == "srv.admintool" {
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

func TestBuildEntry_annotationsThreaded(t *testing.T) {
	raw := json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false}`)
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: "svc",
		Defs: []transport.ToolDefinition{
			{Name: "get_data", Description: "desc", InputSchema: json.RawMessage(`{}`), Annotations: raw},
		},
		Perm: nil,
	})

	e, err := reg.Lookup("svc.get_data")
	if err != nil {
		t.Fatal(err)
	}
	if string(e.Def.Annotations) != string(raw) {
		t.Errorf("annotations not threaded through buildEntry: got %s, want %s", e.Def.Annotations, raw)
	}
}
