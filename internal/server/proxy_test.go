//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type addProxyConnParams struct {
	T          *testing.T
	Server     *server.Server
	Name       string
	Connection *transport.FakeConnection
}

func addProxyConn(params addProxyConnParams) {
	params.T.Helper()
	writeTestServerConfig(params.T, params.Server, config.ServerConfig{Name: params.Name})
	if err := params.Server.AddConnection(
		context.Background(),
		config.ServerConfig{Name: params.Name},
		params.Connection,
	); err != nil {
		params.T.Fatalf("AddConnection %s: %v", params.Name, err)
	}
}

func TestProxy_Initialize_Instructions(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	msgs := serveAll(t, srv)
	var initResult map[string]any
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if _, hasProto := res["protocolVersion"]; hasProto {
				initResult = res
			}
		}
	}
	if initResult == nil {
		t.Fatal("no initialize result")
	}
	const want = "Results are the upstream server's own, trimmed where large: a trimmed result carries `__mini` " +
		"(a key in the result object, the last element of a result list, or a final text block) with a `file` and the paths that were cut. " +
		"To get cut data, call `read` with that `file` and a listed path as `filter`, or a jq filter " +
		"built on it. Pass `__mini: {\"projection\": \"raw\"}` only from a script that must process the " +
		"whole result without a second call. Tool arguments are the upstream server's own; `__mini` is " +
		"reserved."
	if got := initResult["instructions"].(string); got != want {
		t.Errorf("proxy instructions = %q, want %q", got, want)
	}
}

func TestProxy_Call_NoProjection_PassesRawJSON(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"name":"alice"}`), "get_user")

	text := h.callText("svc__get_user", nil)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("expected raw proxy without __mini envelope: %s", text)
	}
	if !json.Valid([]byte(text)) {
		t.Errorf("expected valid JSON proxy: %s", text)
	}
}

func TestProxy_Call_NoProjection_DefaultStringLimitApplies(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultStringLimit = 10
	h := newProxyHarness(t, newTestServer(t, server.Params{Config: cfg}))
	longVal := strings.Repeat("x", 80)
	h.addUpstream("svc", textResult(`{"id":1,"body":"`+longVal+`"}`), "get_item")

	if text := h.callText("svc__get_item", nil); strings.Contains(text, longVal) {
		t.Errorf("expected DefaultStringLimit to truncate long string, but got full value in: %s", text)
	}
}

func TestProxy_Call_WithProjection_ElisionInlinesPlusFile(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("gh", textResult(`{"id":1,"secret":"hidden"}`), "list_repos")
	h.setProjection("gh", "list_repos", map[string]any{"exclude": []string{"secret"}})

	text := h.callText("gh__list_repos", nil)

	env := parseProxyEnvelope(t, text)
	if !env.HasMini {
		t.Fatalf("expected __mini key in envelope: %s", text)
	}
	if len(env.Excluded) == 0 || env.Msg == "" {
		t.Errorf("expected __mini.excluded and __mini.msg to be set: %s", text)
	}
	if _, hasID := env.Data["id"]; !hasID {
		t.Errorf("expected id in the upstream object, got: %v", env.Data)
	}
	if _, hasSecret := env.Data["secret"]; hasSecret {
		t.Errorf("expected secret to be excluded from the upstream object, got: %v", env.Data)
	}
}

func TestProxy_NestedExclusion_ReportsExactLeafPath(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("gh", textResult(`{"item":{"id":1,"body":"long body text here"}}`), "list_prs")
	h.setProjection("gh", "list_prs", map[string]any{"exclude": []string{"item.body"}})

	text := h.callText("gh__list_prs", nil)

	env := parseProxyEnvelope(t, text)
	if !env.HasMini {
		t.Fatal("expected __mini envelope for nested exclusion")
	}
	if !strings.Contains(text, ".item.body") {
		t.Errorf("expected excluded path .item.body in __mini envelope, got: %s", text)
	}
	if item, ok := env.Data["item"].(map[string]any); !ok || item["id"] != float64(1) || item["body"] != nil {
		t.Errorf("expected item identity without body, got: %v", env.Data)
	}
	if env.File == "" {
		t.Errorf("expected __mini.file for nested exclusion (raw file must be written), got: %s", text)
	}
}

func TestProxy_IncludeFilter_PassthroughWhenAllFieldsIncluded(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"value":"data"}`), "get_data")
	h.setProjection("svc", "get_data", map[string]any{"include_only": []string{"id", "value"}})

	text := h.callText("svc__get_data", nil)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("expected no __mini envelope when nothing excluded: %s", text)
	}
	env := parseProxyEnvelope(t, text)
	if env.Data["id"] != float64(1) || env.Data["value"] != "data" {
		t.Errorf("expected id:1 and value:data, got %v", env.Data)
	}
}

func TestProxy_Call_WithTruncation_ProjectionNote(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("gh", textResult(`{"id":1,"body":"this is a very long body that will be truncated"}`), "get_issue")
	h.setProjection("gh", "get_issue", map[string]any{"string_limits": map[string]any{"body": 5}})

	text := h.callText("gh__get_issue", nil)

	env := parseProxyEnvelope(t, text)
	if len(env.Truncated) == 0 {
		t.Fatalf("expected __mini.truncated to be non-empty: %s", text)
	}
	if path := env.Truncated[0].Path; path != ".content[0].text | fromjson | .body" {
		t.Errorf("expected truncated[0].path = .content[0].text | fromjson | .body, got %q", path)
	}
	if env.Truncated[0].Chars == 0 || env.Msg == "" {
		t.Errorf("expected truncated[0].chars > 0 and __mini.msg to be set: %s", text)
	}
}

func TestProxy_Call_WithExclusionAndTruncation(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("gh", textResult(
		`{"id":1,"secret":"hidden","body":"this is a very long body that will be truncated by the limit"}`,
	), "get_issue")
	h.setProjection("gh", "get_issue", map[string]any{
		"exclude":       []string{"secret"},
		"string_limits": map[string]any{"body": 5},
	})

	text := h.callText("gh__get_issue", nil)

	env := parseProxyEnvelope(t, text)
	if len(env.Excluded) == 0 || len(env.Truncated) == 0 || env.Msg == "" {
		t.Errorf("expected __mini.excluded, __mini.truncated and __mini.msg: %s", text)
	}
}

func TestProxy_Call_ToolFormatToon_RendersToon(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"name":"alice"}`), "get_user")
	h.setProjection("svc", "get_user", map[string]any{"format": "toon"})

	text := h.callText("svc__get_user", nil)

	if strings.HasPrefix(text, "{") || !strings.Contains(text, "name: alice") {
		t.Errorf("per-tool format:toon should produce TOON with the name field, got: %s", text)
	}
}

func TestProxy_Call_GlobalFormatToon_RendersToonUnlessTheToolSaysJSON(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = "toon"
	h := newProxyHarness(t, newTestServer(t, server.Params{Config: cfg}))
	h.addUpstream("svc", textResult(`{"id":1,"name":"alice"}`), "get_user", "get_raw_user")
	h.setProjection("svc", "get_raw_user", map[string]any{"format": "json"})

	text := h.callText("svc__get_user", nil)
	if strings.HasPrefix(text, "{") || !strings.Contains(text, "name: alice") {
		t.Errorf("global response_format:toon should produce TOON in proxy mode, got: %s", text)
	}
	if text := h.callText("svc__get_raw_user", nil); !strings.HasPrefix(text, "{") {
		t.Errorf("per-tool format:json should override global toon, got: %s", text)
	}
}

func TestProxy_Call_SessionFormatToon_RendersToon(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`[{"id":1,"name":"foo"},{"id":2,"name":"bar"}]`), "list_items")
	session := h.session("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
	session.setProjection("svc", "list_items", map[string]any{"format": "toon"})

	if text := session.callText("svc__list_items", nil); strings.HasPrefix(text, "{") {
		t.Errorf("session format:toon should produce TOON in proxy mode, got JSON: %s", text)
	}
}
