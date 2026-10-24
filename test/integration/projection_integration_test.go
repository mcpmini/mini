//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestIntegrationProjection_excludeAlways(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1,"title":"hello","node_id":"abc","internal_ref":"xyz"}`},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				Exclude: []string{"node_id", "internal_ref"},
			},
		},
	})

	e := client.execResult("svc", "get_item", nil)

	b, _ := json.Marshal(e.Data)
	if strings.Contains(string(b), "node_id") || strings.Contains(string(b), "internal_ref") {
		t.Errorf("exclude fields should be absent, got: %s", b)
	}
	if !strings.Contains(string(b), "title") {
		t.Errorf("non-excluded field 'title' should remain, got: %s", b)
	}
}

func TestIntegrationProjection_elidedFieldsReported(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1,"title":"hello","node_id":"abc"}`},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				Exclude: []string{"node_id"},
			},
		},
	})

	e := client.execResult("svc", "get_item", nil)

	found := false
	for _, v := range e.Notice.Excluded {
		if v == ".content[0].text | fromjson | .node_id" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '.node_id' in excluded list, got: %v", e.Notice.Excluded)
	}
}

func TestIntegrationProjection_includeOnly(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{
			"get_item": `{"id":1,"title":"hello","body":"long text","created_at":"2024-01-01"}`,
		},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				IncludeOnly: []string{"id", "title"},
			},
		},
	})

	b, _ := json.Marshal(client.execResult("svc", "get_item", nil).Data)
	data := string(b)
	if strings.Contains(data, "body") || strings.Contains(data, "created_at") {
		t.Errorf("non-included fields should be absent, got: %s", data)
	}
	if !strings.Contains(data, `"id"`) || !strings.Contains(data, `"title"`) {
		t.Errorf("included fields should be present, got: %s", data)
	}
}

func TestIntegrationProjection_stringLimit(t *testing.T) {
	t.Parallel()
	longStr := strings.Repeat("x", 500)
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1,"body":"` + longStr + `"}`},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				StringLimits: map[string]int{"body": 50},
			},
		},
	})

	b, _ := json.Marshal(client.execResult("svc", "get_item", nil).Data)
	if strings.Contains(string(b), longStr) {
		t.Errorf("string_limit should have truncated the body field")
	}
}

func TestIntegrationProjection_omittedEnvelope(t *testing.T) {
	t.Parallel()
	longStr := strings.Repeat("w", 400)
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1,"title":"short","body":"` + longStr + `"}`},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				StringLimits: map[string]int{"body": 60},
			},
		},
	})

	env := client.execResult("svc", "get_item", nil)
	if env.IsError {
		t.Fatalf("expected success, got error: %s", env.Text)
	}

	if len(env.Notice.Truncated) != 1 || env.Notice.Truncated[0].Path != ".content[0].text | fromjson | .body" ||
		env.Notice.Truncated[0].Chars <= 0 {
		t.Fatalf("expected one truncated .body entry, got %v", env.Notice.Truncated)
	}
	// 400 chars → limit 60, so at least 300 chars removed
	if env.Notice.Truncated[0].Chars < 300 {
		t.Errorf("expected at least 300 chars removed from body, got %d", env.Notice.Truncated[0].Chars)
	}
	for _, o := range env.Notice.Truncated {
		if o.Path == ".content[0].text | fromjson | .title" {
			t.Errorf("short field 'title' should not appear in truncated")
		}
	}
	// file written because truncation counts as projection
	if env.Notice.File == "" {
		t.Error("expected file path in envelope when truncation applied")
	}
}

func TestIntegrationProjection_arrayLimit(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{
			"get_repo": `{"issues":[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5}],"name":"repo"}`,
		},
		Projections: map[string]*config.ProjectionConfig{
			"get_repo": {
				ArrayLimits: map[string]int{"issues": 3},
			},
		},
	})

	b, _ := json.Marshal(client.execResult("svc", "get_repo", nil).Data)
	data := string(b)
	for _, id := range []string{`"id":5`, `"id":4`} {
		if strings.Contains(data, id) {
			t.Errorf("array_limits.issues:3 should cap at 3 items, still found %s in: %s", id, data)
		}
	}
}

func TestIntegrationProjection_wildcardAppliesAllTools(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{
			"get_a": `{"id":1,"node_id":"abc","title":"a"}`,
			"get_b": `{"id":2,"node_id":"def","title":"b"}`,
		},
		Projections: map[string]*config.ProjectionConfig{"*": {Exclude: []string{"node_id"}}},
	})

	for _, tool := range []string{"get_a", "get_b"} {
		b, _ := json.Marshal(client.execResult("svc", tool, nil).Data)
		if strings.Contains(string(b), "node_id") {
			t.Errorf("tool %s: wildcard exclude should remove node_id, got: %s", tool, b)
		}
	}
}

func TestIntegrationProjection_inlineInServerYAML(t *testing.T) {
	t.Parallel()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"node_id":"abc","title":"hello"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, fakeServerParams{
		ServerName:  "svc",
		Fixtures:    dir,
		Projections: map[string]*config.ProjectionConfig{"get_item": {Exclude: []string{"node_id"}}},
	})

	b, _ := json.Marshal(startServer(t, cfg).execResult("svc", "get_item", nil).Data)
	if strings.Contains(string(b), "node_id") {
		t.Errorf("inline server YAML projection should exclude node_id, got: %s", b)
	}
}

func TestIntegrationProjection_sessionOverridesServerLevel(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1,"title":"hello","body":"long content","extra":"strip this"}`},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				IncludeOnly: []string{"id", "title"},
			},
		},
	})
	client.setProjection(setProjectionParams{
		Server:      "svc",
		Tool:        "get_item",
		Projection:  map[string]any{"include_only": []string{"id"}},
		SessionOnly: true,
	})

	b, _ := json.Marshal(client.execResult("svc", "get_item", nil).Data)
	if strings.Contains(string(b), "title") {
		t.Errorf("session override should suppress 'title', got: %s", b)
	}
}

func TestIntegrationProjection_configurePersistsAcrossCalls(t *testing.T) {
	t.Parallel()
	client := quickServer(t, map[string]string{"get_item": `{"id":1,"title":"hello","secret":"hidden"}`})
	client.setProjection(setProjectionParams{
		Server:      "svc",
		Tool:        "get_item",
		Projection:  map[string]any{"exclude": []string{"secret"}},
		SessionOnly: true,
	})

	for i := range 2 {
		b, _ := json.Marshal(client.execResult("svc", "get_item", nil).Data)
		if strings.Contains(string(b), "secret") {
			t.Errorf("call %d: projection should persist — 'secret' should be excluded, got: %s", i+1, b)
		}
	}
}

func TestIntegrationProjection_toolSpecificOverridesWildcard(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{
			"get_a": `{"id":1,"node_id":"abc","title":"a"}`,
			"get_b": `{"id":2,"node_id":"def","title":"b"}`,
		},
		Projections: map[string]*config.ProjectionConfig{"*": {
			Exclude: []string{"node_id"},
		}, "get_b": {
			IncludeOnly: []string{"id", "node_id", "title"},
		}},
	})

	bA, _ := json.Marshal(client.execResult("svc", "get_a", nil).Data)
	if strings.Contains(string(bA), "node_id") {
		t.Errorf("get_a: wildcard should exclude node_id, got: %s", bA)
	}

	bB, _ := json.Marshal(client.execResult("svc", "get_b", nil).Data)
	if !strings.Contains(string(bB), "node_id") {
		t.Errorf("get_b: tool-specific config should retain node_id, got: %s", bB)
	}
}

func TestIntegrationProjection_persistToDisk(t *testing.T) {
	t.Parallel()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"title":"hello","secret":"hidden"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})

	client1 := startServer(t, cfg)
	client1.setProjection(setProjectionParams{
		Server:      "svc",
		Tool:        "get_item",
		Projection:  map[string]any{"exclude": []string{"secret"}},
		SessionOnly: false,
	})

	client2 := startServer(t, cfg)
	b, _ := json.Marshal(client2.execResult("svc", "get_item", nil).Data)
	if strings.Contains(string(b), "secret") {
		t.Errorf("persisted projection should suppress 'secret' for new session, got: %s", b)
	}
}

func TestIntegrationProjection_depthLimit(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"a":{"b":{"c":{"d":"deep"}}}}`},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				DepthLimit: 2,
			},
		},
	})

	b, _ := json.Marshal(client.execResult("svc", "get_item", nil).Data)
	if strings.Contains(string(b), `"deep"`) {
		t.Errorf("depth_limit should have replaced deep field, got: %s", b)
	}
	if !strings.Contains(string(b), "depth") && !strings.Contains(string(b), "limit") &&
		!strings.Contains(string(b), "...") {
		t.Errorf("depth_limit placeholder should be present in response, got: %s", b)
	}
}

func TestIntegrationProjection_includeAndExclude(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1,"title":"hello","node_id":"abc"}`},
		Projections: map[string]*config.ProjectionConfig{
			"get_item": {
				IncludeOnly: []string{"id", "title", "node_id"},
				Exclude:     []string{"node_id"},
			},
		},
	})

	b, _ := json.Marshal(client.execResult("svc", "get_item", nil).Data)
	data := string(b)
	if strings.Contains(data, "node_id") {
		t.Errorf("exclude should remove node_id even if it's in include, got: %s", data)
	}
	if !strings.Contains(data, `"title"`) || !strings.Contains(data, `"id"`) {
		t.Errorf("include fields id and title should remain, got: %s", data)
	}
}

func TestIntegrationProjection_globalDefaultsApply(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.DefaultStringLimit = 50
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"get_item": `{"id":1,"description":"` + strings.Repeat("x", 300) + `"}`},
		Config:   cfg,
	})

	b, _ := json.Marshal(client.execResult("svc", "get_item", nil).Data)
	data := string(b)
	if strings.Contains(data, strings.Repeat("x", 100)) {
		t.Errorf("global default_string_limit:50 should truncate long strings, got: %s", data[:min(200, len(data))])
	}
}

type toolExcludesParams struct {
	Client *mcpClient
	Server string
	Tool   string
	Field  string
}

func assertToolExcludes(t *testing.T, p toolExcludesParams) {
	t.Helper()
	b, _ := json.Marshal(p.Client.execResult(p.Server, p.Tool, nil).Data)
	if strings.Contains(string(b), p.Field) {
		t.Errorf("%s.%s should exclude field %q", p.Server, p.Tool, p.Field)
	}
}

func TestIntegrationProjection_persistMergesWithExistingYAML(t *testing.T) {
	t.Parallel()
	dir := mockFixtureDir(t, map[string]string{
		"tool_a": `{"id":1,"secret_a":"x","other":"y"}`,
		"tool_b": `{"id":2,"secret_b":"x","other":"y"}`,
	})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	c1 := startServer(t, cfg)
	c1.setProjection(setProjectionParams{
		Server:      "svc",
		Tool:        "tool_a",
		Projection:  map[string]any{"exclude": []string{"secret_a"}},
		SessionOnly: false,
	})
	c2 := startServer(t, cfg)
	c2.setProjection(setProjectionParams{
		Server:      "svc",
		Tool:        "tool_b",
		Projection:  map[string]any{"exclude": []string{"secret_b"}},
		SessionOnly: false,
	})
	c3 := startServer(t, cfg)
	assertToolExcludes(t, toolExcludesParams{
		Client: c3,
		Server: "svc",
		Tool:   "tool_a",
		Field:  "secret_a",
	})
	assertToolExcludes(t, toolExcludesParams{
		Client: c3,
		Server: "svc",
		Tool:   "tool_b",
		Field:  "secret_b",
	})
}

func TestIntegrationProjection_persistDoesNotAffectRunningSession(t *testing.T) {
	t.Parallel()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	c1 := startServer(t, cfg)
	c2 := startServer(t, cfg)
	b2, _ := json.Marshal(c2.execResult("svc", "get_item", nil).Data)
	if !strings.Contains(string(b2), "secret") {
		t.Fatal("expected secret field before projection")
	}
	c1.setProjection(setProjectionParams{
		Server:      "svc",
		Tool:        "get_item",
		Projection:  map[string]any{"exclude": []string{"secret"}},
		SessionOnly: false,
	})
	assertSessionIsolation(t, cfg, c2)
}

func TestIntegrationProjection_toonFormat(t *testing.T) {
	t.Parallel()
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"list_items": `[{"id":1,"name":"foo"},{"id":2,"name":"bar"}]`},
		Projections: map[string]*config.ProjectionConfig{
			"list_items": {
				Format: "toon",
			},
		},
	})

	text := client.execTool("svc", "list_items", nil)
	if !strings.Contains(text, "[2]{id,name}:") {
		t.Errorf("toon format should render a tabular block, got: %s", text)
	}
}

func TestIntegrationProjection_toonFormatGlobal(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = config.FormatToon
	client := quickServerWith(t, quickServerParams{
		Fixtures: map[string]string{"list_items": `[{"id":1,"name":"foo"},{"id":2,"name":"bar"}]`},
		Config:   cfg,
	})

	text := client.execTool("svc", "list_items", nil)
	if !strings.Contains(text, "[2]{id,name}:") {
		t.Errorf("global response_format:toon should render a tabular block, got: %s", text)
	}
}
