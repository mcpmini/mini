//go:build test

package server_test

import (
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newProxyHarnessWithSecretTool(t *testing.T, stringLimit int) *proxyHarness {
	t.Helper()
	cfg := config.DefaultConfig()
	if stringLimit > 0 {
		cfg.DefaultStringLimit = stringLimit
	}
	h := newProxyHarness(t, newTestServer(t, server.Params{Config: cfg}))
	h.addUpstream("svc", textResult(`{"id":1,"secret":"hidden","body":"`+strings.Repeat("x", 80)+`"}`), "get_item")
	return h
}

var (
	rawProjection     = map[string]any{"__mini": map[string]any{"projection": "raw"}}
	defaultProjection = map[string]any{"__mini": map[string]any{"projection": "default"}}
	excludeSecret     = map[string]any{"exclude": []string{"secret"}}
)

func TestProxy_RawProjection_BypassesConfiguredExclusion(t *testing.T) {
	h := newProxyHarnessWithSecretTool(t, 0)
	h.setProjection("svc", "get_item", excludeSecret)

	text := h.callText("svc__get_item", rawProjection)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("raw projection must produce no __mini metadata, got: %s", text)
	}
	if env := parseProxyEnvelope(t, text); env.Data["secret"] != "hidden" {
		t.Errorf("raw projection must bypass configured exclusion, got data: %v", env.Data)
	}
}

func TestProxy_RawProjection_BypassesGlobalStringLimit(t *testing.T) {
	h := newProxyHarnessWithSecretTool(t, 10)

	if text := h.callText("svc__get_item", rawProjection); !strings.Contains(text, strings.Repeat("x", 80)) {
		t.Errorf("raw projection must bypass global DefaultStringLimit, got: %s", text)
	}
}

func TestProxy_DefaultProjection_UsesConfiguredExclusion(t *testing.T) {
	h := newProxyHarnessWithSecretTool(t, 0)
	h.setProjection("svc", "get_item", excludeSecret)

	text := h.callText("svc__get_item", defaultProjection)

	env := parseProxyEnvelope(t, text)
	if !env.HasMini {
		t.Fatalf("expected __mini for default projection with configured exclusion, got: %s", text)
	}
	if _, hasSecret := env.Data["secret"]; hasSecret {
		t.Errorf("default projection must apply configured exclusion, got data: %v", env.Data)
	}
}

func TestProxy_OmittedProjectionControl_BehavesAsDefault(t *testing.T) {
	h := newProxyHarnessWithSecretTool(t, 0)
	h.setProjection("svc", "get_item", excludeSecret)

	env := parseProxyEnvelope(t, h.callText("svc__get_item", nil))

	if _, hasSecret := env.Data["secret"]; hasSecret {
		t.Errorf("omitted __mini.projection must behave as default, got data: %v", env.Data)
	}
}

func TestProxy_InvalidProjectionControl_RejectedAsToolError(t *testing.T) {
	h := newProxyHarnessWithSecretTool(t, 0)

	resp := serve(t, h.srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "bogus"}}))

	requireRPCError(requireRPCErrorParams{T: t, Resp: resp, WantCode: -32602, WantSubstr: "projection"})
}

func TestProxy_ConcurrentRawAndDefaultCalls_DoNotCrossContaminate(t *testing.T) {
	h := newProxyHarnessWithSecretTool(t, 0)
	h.setProjection("svc", "get_item", excludeSecret)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			env := parseProxyEnvelope(t, h.callText("svc__get_item", rawProjection))
			if env.Data["secret"] != "hidden" {
				t.Errorf("raw call lost secret field under concurrency: %v", env.Data)
			}
		})
		wg.Go(func() {
			env := parseProxyEnvelope(t, h.callText("svc__get_item", defaultProjection))
			if _, hasSecret := env.Data["secret"]; hasSecret {
				t.Errorf("default call leaked secret field under concurrency: %v", env.Data)
			}
		})
	}
	wg.Wait()
}

func TestProxy_ToonFormat_RendersToonOutput(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult(`[{"id":1,"name":"alice"},{"id":2,"name":"bob"}]`), "list_items")

	text := h.callText("svc__list_items", nil)

	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected TOON format in proxy mode, got JSON: %s", text)
	}
	if !strings.Contains(text, "[2]{id,name}:") {
		t.Errorf("expected tabular TOON block, got: %s", text)
	}
}

func TestProxy_DefaultProjection_PreservesLargeIntegers(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":9007199254740993,"secret":"hidden"}`), "get_item")
	h.setProjection("svc", "get_item", excludeSecret)

	if text := h.callText("svc__get_item", nil); !strings.Contains(text, "9007199254740993") {
		t.Errorf("default projection corrupted large integer: %s", text)
	}
}

func newProxyToonServer(t *testing.T, logs *syncBuffer) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = "toon"
	params := server.Params{Config: cfg}
	if logs != nil {
		params.Logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	return newTestServer(t, params)
}

func TestProxy_ToonFormat_UntrimmedObjectTextBecomesOneToonBlock(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult(`{"id":1,"name":"alice"}`), "get_user")

	result := h.call("svc__get_user", nil)

	if _, ok := result["structuredContent"]; ok {
		t.Errorf("TOON must not add structuredContent: %v", result)
	}
	if texts := contentTexts(result); len(texts) != 1 || !strings.Contains(texts[0], "name: alice") {
		t.Errorf("want one TOON text block, got %q", texts)
	}
}

func TestProxy_ToonFormat_TrimmedArrayKeepsJSONNoticeBlock(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult(`[{"id":1,"secret":"x"},{"id":2,"secret":"y"}]`), "list_users")
	h.setProjection("svc", "list_users", map[string]any{"array_limits": map[string]int{"default": 1}})

	texts := contentTexts(h.call("svc__list_users", nil))

	if len(texts) != 2 {
		t.Fatalf("want TOON block plus notice block, got %q", texts)
	}
	if texts[0] != "[1]{id,secret}:\n  1,x" {
		t.Errorf("want TOON array with one intact element, got %q", texts[0])
	}
	if !strings.HasPrefix(texts[1], "\n{\"__mini\":") {
		t.Errorf("want JSON notice block last, got %q", texts[1])
	}
}

func TestProxy_ToonFormat_KeepsUpstreamKeyOrder(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult(`{"name":"alice","id":1}`), "get_user")

	if texts := contentTexts(h.call("svc__get_user", nil)); len(texts) != 1 || texts[0] != "name: alice\nid: 1" {
		t.Errorf("want TOON in upstream key order, got %q", texts)
	}
}

func TestProxy_ToonFormat_EmptyObjectKeepsJSONText(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult(`{}`), "delete_user")

	if texts := contentTexts(h.call("svc__delete_user", nil)); len(texts) != 1 || texts[0] != "{}" {
		t.Errorf("want the upstream {} text kept, got %q", texts)
	}
}

func TestProxy_ToonFormat_TrimmedObjectCarriesNoticeInToon(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult(`{"id":1,"secret":"x"}`), "get_user")
	h.setProjection("svc", "get_user", excludeSecret)

	texts := contentTexts(h.call("svc__get_user", nil))

	if len(texts) != 1 {
		t.Fatalf("want one TOON block carrying the notice, got %q", texts)
	}
	if !strings.Contains(texts[0], "__mini:") || strings.Contains(texts[0], `"secret"`) ||
		strings.HasPrefix(texts[0], "{") {
		t.Errorf("want TOON object with __mini and no secret, got %q", texts[0])
	}
}

func TestProxy_ToonFormat_MirrorTextIsToonAndStructuredKeepsNotice(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc",
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}],"structuredContent":{"id":1,"secret":"x"}}`,
		"get_user")
	h.setProjection("svc", "get_user", excludeSecret)

	result := h.call("svc__get_user", nil)

	structured, _ := result["structuredContent"].(map[string]any)
	if _, ok := structured["__mini"]; !ok {
		t.Errorf("structuredContent must carry __mini: %v", structured)
	}
	texts := contentTexts(result)
	if len(texts) != 1 || strings.HasPrefix(texts[0], "{") || strings.Contains(texts[0], "secret: ") {
		t.Errorf("want one TOON mirror block without secret, got %q", texts)
	}
}

func TestProxy_ToonFormat_ProseTextIsUnchanged(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", textResult("hello, world"), "describe")

	if texts := contentTexts(h.call("svc__describe", nil)); len(texts) != 1 || texts[0] != "hello, world" {
		t.Errorf("prose text must pass through unchanged, got %q", texts)
	}
}

func TestProxy_ToonFormat_UnencodableJSONKeepsJSONAndLogsWarning(t *testing.T) {
	var logs syncBuffer
	h := newProxyHarness(t, newProxyToonServer(t, &logs))
	h.addUpstream("svc", `{"content":[{"type":"text","text":`+deepPayload(t)+`}]}`, "get_blob")

	texts := contentTexts(h.call("svc__get_blob", nil))

	if len(texts) != 1 || !strings.Contains(texts[0], "depth-sentinel") {
		t.Errorf("unencodable JSON must stay as JSON text, got %.80q", texts)
	}
	if !strings.Contains(logs.String(), "toon encode failed") {
		t.Errorf("want a toon encode warning, got logs: %s", logs.String())
	}
}

func TestProxy_ToonFormat_RawAndErrorResultsStayJSON(t *testing.T) {
	t.Run("raw projection", func(t *testing.T) {
		h := newProxyHarness(t, newProxyToonServer(t, nil))
		h.addUpstream("svc", textResult(`{"id":1,"name":"alice"}`), "get_user")

		if text := h.callText("svc__get_user", rawProjection); text != `{"id":1,"name":"alice"}` {
			t.Errorf("raw result must stay JSON, got %q", text)
		}
	})
	t.Run("isError", func(t *testing.T) {
		h := newProxyHarness(t, newProxyToonServer(t, nil))
		h.addUpstream("svc", `{"content":[{"type":"text","text":"{\"code\":7}"}],"isError":true}`, "fail")

		if text := h.callText("svc__fail", nil); text != `{"code":7}` {
			t.Errorf("isError result must stay JSON, got %q", text)
		}
	})
}

func TestProxy_ToonFormat_PerToolOverrideOnlyAffectsThatTool(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"name":"alice"}`), "toon_tool", "json_tool")
	h.setProjection("svc", "toon_tool", map[string]any{"format": "toon"})

	toonText := contentTexts(h.call("svc__toon_tool", nil))
	jsonText := contentTexts(h.call("svc__json_tool", nil))

	if len(toonText) != 1 || strings.HasPrefix(toonText[0], "{") {
		t.Errorf("configured tool must be TOON, got %q", toonText)
	}
	if len(jsonText) != 1 || !strings.HasPrefix(jsonText[0], "{") {
		t.Errorf("other tool must stay JSON, got %q", jsonText)
	}
}

func TestProxy_ArrayItemExclusion_TrimsEachEntryAndKeepsRecovery(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{Config: config.DefaultConfig()}))
	h.addUpstream("svc", textResult(
		`[{"sha":"a","author":{"login":"x","avatar_url":"u1"}},{"sha":"b","author":{"login":"y","avatar_url":"u2"}}]`,
	), "list_commits")
	h.setProjection("svc", "list_commits", map[string]any{"exclude": []string{"[].author.avatar_url"}})

	texts := contentTexts(h.call("svc__list_commits", nil))

	if len(texts) != 2 || texts[0] != `[{"author":{"login":"x"},"sha":"a"},{"author":{"login":"y"},"sha":"b"}]` {
		t.Fatalf("want trimmed entries plus a notice block, got %q", texts)
	}
	notice := parseProxyEnvelope(t, texts[1])
	const path = ".content[0].text | fromjson | .[].author.avatar_url"
	if len(notice.Excluded) != 1 || notice.Excluded[0] != path {
		t.Fatalf("excluded = %q, want [%q]", notice.Excluded, path)
	}
	recovered := h.callText("read", map[string]any{"file": notice.File, "filter": path})
	if !strings.Contains(recovered, "u1") || !strings.Contains(recovered, "u2") {
		t.Errorf("read(%q) = %q, want both original avatar URLs", path, recovered)
	}
}
