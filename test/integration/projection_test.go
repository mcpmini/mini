//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjection_excludeAlways(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"title":"hello","node_id":"abc","internal_ref":"xyz"}`},
		"", "get_item:\n  exclude_always: [node_id, internal_ref]\n")

	e := client.execEnvelope("svc", "get_item", nil)

	b, _ := json.Marshal(e.Data)
	if strings.Contains(string(b), "node_id") || strings.Contains(string(b), "internal_ref") {
		t.Errorf("exclude_always fields should be absent, got: %s", b)
	}
	if !strings.Contains(string(b), "title") {
		t.Errorf("non-excluded field 'title' should remain, got: %s", b)
	}
}

func TestProjection_elidedFieldsReported(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"title":"hello","node_id":"abc"}`},
		"", "get_item:\n  exclude_always: [node_id]\n")

	e := client.execEnvelope("svc", "get_item", nil)

	found := false
	for _, v := range e.Elided {
		if v == ".node_id" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '.node_id' in elided list, got: %v", e.Elided)
	}
}

func TestProjection_includeOnly(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"title":"hello","body":"long text","created_at":"2024-01-01"}`},
		"", "get_item:\n  include: [id, title]\n")

	b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
	data := string(b)
	if strings.Contains(data, "body") || strings.Contains(data, "created_at") {
		t.Errorf("non-included fields should be absent, got: %s", data)
	}
	if !strings.Contains(data, `"id"`) || !strings.Contains(data, `"title"`) {
		t.Errorf("included fields should be present, got: %s", data)
	}
}

func TestProjection_stringLimit(t *testing.T) {
	longStr := strings.Repeat("x", 500)
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"body":"` + longStr + `"}`},
		"", "get_item:\n  string_limits:\n    body: 50\n")

	b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b), longStr) {
		t.Errorf("string_limit should have truncated the body field")
	}
}

func TestProjection_omittedEnvelope(t *testing.T) {
	longStr := strings.Repeat("w", 400)
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"title":"short","body":"` + longStr + `"}`},
		"",
		"get_item:\n  string_limits:\n    body: 60\n")

	env := client.execEnvelope("svc", "get_item", nil)
	if env.Error != "" {
		t.Fatalf("expected success, got error: %s", env.Error)
	}

	if len(env.Truncated) != 1 || env.Truncated[0].JQPath != ".body" || env.Truncated[0].Chars <= 0 {
		t.Fatalf("expected one omitted .body entry, got %v", env.Truncated)
	}
	// 400 chars → limit 60, so at least 300 chars removed
	if env.Truncated[0].Chars < 300 {
		t.Errorf("expected at least 300 chars removed from body, got %d", env.Truncated[0].Chars)
	}
	for _, o := range env.Truncated {
		if o.JQPath == ".title" {
			t.Errorf("short field 'title' should not appear in omitted")
		}
	}
	// file written because truncation counts as projection
	if env.File == nil {
		t.Error("expected file path in envelope when truncation applied")
	}
}

func TestProjection_arrayLimit(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_repo": `{"issues":[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5}],"name":"repo"}`},
		"",
		"get_repo:\n  array_limits:\n    issues: 3\n")

	b, _ := json.Marshal(client.execEnvelope("svc", "get_repo", nil).Data)
	data := string(b)
	for _, id := range []string{`"id":5`, `"id":4`} {
		if strings.Contains(data, id) {
			t.Errorf("array_limits.issues:3 should cap at 3 items, still found %s in: %s", id, data)
		}
	}
}

func TestProjection_wildcardAppliesAllTools(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{
			"get_a": `{"id":1,"node_id":"abc","title":"a"}`,
			"get_b": `{"id":2,"node_id":"def","title":"b"}`,
		},
		"", "\"*\":\n  exclude_always: [node_id]\n")

	for _, tool := range []string{"get_a", "get_b"} {
		b, _ := json.Marshal(client.execEnvelope("svc", tool, nil).Data)
		if strings.Contains(string(b), "node_id") {
			t.Errorf("tool %s: wildcard exclude_always should remove node_id, got: %s", tool, b)
		}
	}
}

func TestProjection_inlineInServerYAML(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"node_id":"abc","title":"hello"}`})
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "svc", "name: svc\ncommand: "+fakemcpBin+
		"\nargs:\n  - --fixtures\n  - "+dir+"\nprojections:\n  get_item:\n    exclude_always: [node_id]\n")

	b, _ := json.Marshal(startServer(t, cfg).execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b), "node_id") {
		t.Errorf("inline server YAML projection should exclude node_id, got: %s", b)
	}
}

func TestProjection_sessionOverridesServerLevel(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"title":"hello","body":"long content","extra":"strip this"}`},
		"", "get_item:\n  include: [id, title]\n")
	client.setProjection("svc", "get_item", map[string]any{"include": []string{"id"}}, true)

	b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b), "title") {
		t.Errorf("session override should suppress 'title', got: %s", b)
	}
}

func TestProjection_configurePersistsAcrossCalls(t *testing.T) {
	client := quickServer(t, map[string]string{"get_item": `{"id":1,"title":"hello","secret":"hidden"}`})
	client.setProjection("svc", "get_item", map[string]any{"exclude_always": []string{"secret"}}, true)

	for i := range 2 {
		b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
		if strings.Contains(string(b), "secret") {
			t.Errorf("call %d: projection should persist — 'secret' should be excluded, got: %s", i+1, b)
		}
	}
}

func TestProjection_toolSpecificOverridesWildcard(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{
			"get_a": `{"id":1,"node_id":"abc","title":"a"}`,
			"get_b": `{"id":2,"node_id":"def","title":"b"}`,
		},
		"", "\"*\":\n  exclude_always: [node_id]\nget_b:\n  include: [id, node_id, title]\n")

	bA, _ := json.Marshal(client.execEnvelope("svc", "get_a", nil).Data)
	if strings.Contains(string(bA), "node_id") {
		t.Errorf("get_a: wildcard should exclude node_id, got: %s", bA)
	}

	bB, _ := json.Marshal(client.execEnvelope("svc", "get_b", nil).Data)
	if !strings.Contains(string(bB), "node_id") {
		t.Errorf("get_b: tool-specific config should retain node_id, got: %s", bB)
	}
}

func TestProjection_persistToDisk(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"title":"hello","secret":"hidden"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)

	client1 := startServer(t, cfg)
	client1.setProjection("svc", "get_item", map[string]any{"exclude_always": []string{"secret"}}, false)

	client2 := startServer(t, cfg)
	b, _ := json.Marshal(client2.execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b), "secret") {
		t.Errorf("persisted projection should suppress 'secret' for new session, got: %s", b)
	}
}

func TestProjection_depthLimit(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"a":{"b":{"c":{"d":"deep"}}}}`},
		"", "get_item:\n  depth_limit: 2\n")

	b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b), `"deep"`) {
		t.Errorf("depth_limit should have replaced deep field, got: %s", b)
	}
	if !strings.Contains(string(b), "depth") && !strings.Contains(string(b), "limit") && !strings.Contains(string(b), "...") {
		t.Errorf("depth_limit placeholder should be present in response, got: %s", b)
	}
}

func TestProjection_passthrough(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"title":"hello","internal_ref":"xyz"}`},
		"", "get_item:\n  include: [id, title]\n  passthrough: [internal_ref]\n")

	e := client.execEnvelope("svc", "get_item", nil)
	if _, ok := e.Passthrough["internal_ref"]; !ok {
		t.Errorf("passthrough field should be in Passthrough map, got: %+v", e.Passthrough)
	}
}

func TestProjection_includeAndExcludeAlways(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"title":"hello","node_id":"abc"}`},
		"", "get_item:\n  include: [id, title, node_id]\n  exclude_always: [node_id]\n")

	b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
	data := string(b)
	if strings.Contains(data, "node_id") {
		t.Errorf("exclude_always should remove node_id even if it's in include, got: %s", data)
	}
	if !strings.Contains(data, `"title"`) || !strings.Contains(data, `"id"`) {
		t.Errorf("include fields id and title should remain, got: %s", data)
	}
}

func TestProjection_globalDefaultsApply(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"get_item": `{"id":1,"description":"` + strings.Repeat("x", 300) + `"}`},
		"default_string_limit: 50\n", "")

	b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
	data := string(b)
	if strings.Contains(data, strings.Repeat("x", 100)) {
		t.Errorf("global default_string_limit:50 should truncate long strings, got: %s", data[:min(200, len(data))])
	}
}

func assertToolExcludes(t *testing.T, client *mcpClient, server, tool, field string) {
	t.Helper()
	b, _ := json.Marshal(client.execEnvelope(server, tool, nil).Data)
	if strings.Contains(string(b), field) {
		t.Errorf("%s.%s should exclude field %q", server, tool, field)
	}
}

func TestProjection_persistMergesWithExistingYAML(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{
		"tool_a": `{"id":1,"secret_a":"x","other":"y"}`,
		"tool_b": `{"id":2,"secret_b":"x","other":"y"}`,
	})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	c1 := startServer(t, cfg)
	c1.setProjection("svc", "tool_a", map[string]any{"exclude_always": []string{"secret_a"}}, false)
	c2 := startServer(t, cfg)
	c2.setProjection("svc", "tool_b", map[string]any{"exclude_always": []string{"secret_b"}}, false)
	c3 := startServer(t, cfg)
	assertToolExcludes(t, c3, "svc", "tool_a", "secret_a")
	assertToolExcludes(t, c3, "svc", "tool_b", "secret_b")
}

func TestProjection_persistDoesNotAffectRunningSession(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	c1 := startServer(t, cfg)
	c2 := startServer(t, cfg)
	b2, _ := json.Marshal(c2.execEnvelope("svc", "get_item", nil).Data)
	if !strings.Contains(string(b2), "secret") {
		t.Fatal("expected secret field before projection")
	}
	c1.setProjection("svc", "get_item", map[string]any{"exclude_always": []string{"secret"}}, false)
	assertSessionIsolation(t, cfg, c2)
}

func TestProjection_linesFormat(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"list_items": `[{"id":1,"name":"foo"},{"id":2,"name":"bar"}]`},
		"", "list_items:\n  format: mini\n")

	text := client.execTool("svc", "list_items", nil)
	if !strings.Contains(text, "[svc.list_items]") {
		t.Errorf("mini format should include tool header [svc.list_items], got: %s", text)
	}
}

func TestProjection_linesFormatGlobal(t *testing.T) {
	client := quickServerWith(t,
		map[string]string{"list_items": `[{"id":1,"name":"foo"},{"id":2,"name":"bar"}]`},
		"response_format: mini\n", "")

	text := client.execTool("svc", "list_items", nil)
	if !strings.Contains(text, "[svc.list_items]") {
		t.Errorf("global response_format:mini should include tool header [svc.list_items], got: %s", text)
	}
}
