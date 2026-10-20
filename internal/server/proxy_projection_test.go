//go:build test

package server_test

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newProxyServerWithSecretTool(t *testing.T, stringLimit int) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	if stringLimit > 0 {
		cfg.DefaultStringLimit = stringLimit
	}
	srv := newTestServer(t, server.Params{Config: cfg})
	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\",\"body\":\"` + strings.Repeat(
			"x",
			80,
		) + `\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	return srv
}

func TestProxy_RawProjection_BypassesConfiguredExclusion(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}}))
	text := toolResultText(t, resp)
	t.Logf("raw response: %s", text)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("raw projection must produce no __mini metadata, got: %s", text)
	}
	env := parseProxyEnvelope(t, text)
	if env.Data["secret"] != "hidden" {
		t.Errorf("raw projection must bypass configured exclusion, got data: %v", env.Data)
	}
}

func TestProxy_RawProjection_BypassesGlobalStringLimit(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 10)
	defer srv.Close()

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}}))
	text := toolResultText(t, resp)

	if !strings.Contains(text, strings.Repeat("x", 80)) {
		t.Errorf("raw projection must bypass global DefaultStringLimit, got: %s", text)
	}
}

func TestProxy_DefaultProjection_UsesConfiguredExclusion(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(
		t,
		srv,
		callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "default"}}),
	)
	text := toolResultText(t, resp)

	env := parseProxyEnvelope(t, text)
	if !env.HasMini {
		t.Fatalf("expected __mini for default projection with configured exclusion, got: %s", text)
	}
	if _, hasSecret := env.Data["secret"]; hasSecret {
		t.Errorf("default projection must apply configured exclusion, got data: %v", env.Data)
	}
}

func TestProxy_OmittedProjectionControl_BehavesAsDefault(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{}))
	text := toolResultText(t, resp)

	env := parseProxyEnvelope(t, text)
	if _, hasSecret := env.Data["secret"]; hasSecret {
		t.Errorf("omitted __mini.projection must behave as default, got data: %v", env.Data)
	}
}

func TestProxy_InvalidProjectionControl_RejectedAsToolError(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	resp := serveProxy(
		t,
		srv,
		callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "bogus"}}),
	)
	requireRPCError(requireRPCErrorParams{T: t, Resp: resp, WantCode: -32602, WantSubstr: "projection"})
}

func TestProxy_ConcurrentRawAndDefaultCalls_DoNotCrossContaminate(t *testing.T) {
	srv := newProxyServerWithSecretTool(t, 0)
	defer srv.Close()

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp := serveProxy(
				t,
				srv,
				callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}}),
			)
			env := parseProxyEnvelope(t, toolResultText(t, resp))
			if env.Data["secret"] != "hidden" {
				t.Errorf("raw call lost secret field under concurrency: %v", env.Data)
			}
		}()
		go func() {
			defer wg.Done()
			resp := serveProxy(
				t,
				srv,
				callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "default"}}),
			)
			env := parseProxyEnvelope(t, toolResultText(t, resp))
			if _, hasSecret := env.Data["secret"]; hasSecret {
				t.Errorf("default call leaked secret field under concurrency: %v", env.Data)
			}
		}()
	}
	wg.Wait()
}

func TestProxy_ToonFormat_RendersToonOutput(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = "toon"
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()
	conn := fakeConn("list_items")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"[{\"id\":1,\"name\":\"alice\"},{\"id\":2,\"name\":\"bob\"}]"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	resp := serveProxy(t, srv, callTool("svc__list_items", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected TOON format in proxy mode, got JSON: %s", text)
	}
	if !strings.Contains(text, "[2]{id,name}:") {
		t.Errorf("expected tabular TOON block, got: %s", text)
	}
}

func TestProxy_DefaultProjection_PreservesLargeIntegers(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":9007199254740993,\"secret\":\"hidden\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "9007199254740993") {
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

type setProxyExcludeParams struct {
	T          *testing.T
	Server     *server.Server
	Tool       string
	Projection map[string]any
}

func setProxyExclude(params setProxyExcludeParams) {
	params.T.Helper()
	serveProxy(params.T, params.Server, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": params.Tool, "projection": params.Projection,
	}))
}

func proxyResultOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result in response: %v", resp)
	}
	return result
}

func contentTexts(result map[string]any) []string {
	blocks, _ := result["content"].([]any)
	texts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		text, _ := b.(map[string]any)["text"].(string)
		texts = append(texts, text)
	}
	return texts
}

func TestProxy_ToonFormat_UntrimmedObjectTextBecomesOneToonBlock(t *testing.T) {
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	result := proxyResultOf(t, serveProxy(t, srv, callTool("svc__get_user", map[string]any{})))
	if _, ok := result["structuredContent"]; ok {
		t.Errorf("TOON must not add structuredContent: %v", result)
	}
	texts := contentTexts(result)
	if len(texts) != 1 || !strings.Contains(texts[0], "name: alice") {
		t.Errorf("want one TOON text block, got %q", texts)
	}
}

func TestProxy_ToonFormat_TrimmedArrayKeepsJSONNoticeBlock(t *testing.T) {
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("list_users")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"[{\"id\":1,\"secret\":\"x\"},{\"id\":2,\"secret\":\"y\"}]"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	setProxyExclude(
		setProxyExcludeParams{
			T:          t,
			Server:     srv,
			Tool:       "list_users",
			Projection: map[string]any{"array_limits": map[string]int{"default": 1}},
		},
	)

	texts := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__list_users", map[string]any{}))))
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
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"name\":\"alice\",\"id\":1}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	texts := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__get_user", map[string]any{}))))
	if len(texts) != 1 || texts[0] != "name: alice\nid: 1" {
		t.Errorf("want TOON in upstream key order, got %q", texts)
	}
}

func TestProxy_ToonFormat_EmptyObjectKeepsJSONText(t *testing.T) {
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("delete_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	texts := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__delete_user", map[string]any{}))))
	if len(texts) != 1 || texts[0] != "{}" {
		t.Errorf("want the upstream {} text kept, got %q", texts)
	}
}

func TestProxy_ToonFormat_TrimmedObjectCarriesNoticeInToon(t *testing.T) {
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	setProxyExclude(
		setProxyExcludeParams{
			T:          t,
			Server:     srv,
			Tool:       "get_user",
			Projection: map[string]any{"exclude": []string{"secret"}},
		},
	)

	texts := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__get_user", map[string]any{}))))
	if len(texts) != 1 {
		t.Fatalf("want one TOON block carrying the notice, got %q", texts)
	}
	if !strings.Contains(texts[0], "__mini:") || strings.Contains(texts[0], `"secret"`) ||
		strings.HasPrefix(texts[0], "{") {
		t.Errorf("want TOON object with __mini and no secret, got %q", texts[0])
	}
}

func TestProxy_ToonFormat_MirrorTextIsToonAndStructuredKeepsNotice(t *testing.T) {
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}],"structuredContent":{"id":1,"secret":"x"}}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	setProxyExclude(
		setProxyExcludeParams{
			T:          t,
			Server:     srv,
			Tool:       "get_user",
			Projection: map[string]any{"exclude": []string{"secret"}},
		},
	)

	result := proxyResultOf(t, serveProxy(t, srv, callTool("svc__get_user", map[string]any{})))
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
	srv := newProxyToonServer(t, nil)
	defer srv.Close()
	conn := fakeConn("describe")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"hello, world"}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	texts := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__describe", map[string]any{}))))
	if len(texts) != 1 || texts[0] != "hello, world" {
		t.Errorf("prose text must pass through unchanged, got %q", texts)
	}
}

func TestProxy_ToonFormat_UnencodableJSONKeepsJSONAndLogsWarning(t *testing.T) {
	var logs syncBuffer
	srv := newProxyToonServer(t, &logs)
	defer srv.Close()
	conn := fakeConn("get_blob")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + deepPayload(t) + `}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	texts := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__get_blob", map[string]any{}))))
	if len(texts) != 1 || !strings.Contains(texts[0], "depth-sentinel") {
		t.Errorf("unencodable JSON must stay as JSON text, got %.80q", texts)
	}
	if !strings.Contains(logs.String(), "toon encode failed") {
		t.Errorf("want a toon encode warning, got logs: %s", logs.String())
	}
}

func TestProxy_ToonFormat_RawAndErrorResultsStayJSON(t *testing.T) {
	t.Run("raw projection", func(t *testing.T) {
		srv := newProxyToonServer(t, nil)
		defer srv.Close()
		conn := fakeConn("get_user")
		conn.Responses["tools/call"] = json.RawMessage(
			`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
		)
		addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

		resp := serveProxy(t, srv, callTool(
			"svc__get_user",
			map[string]any{"__mini": map[string]any{"projection": "raw"}},
		))
		if text := toolResultText(t, resp); text != `{"id":1,"name":"alice"}` {
			t.Errorf("raw result must stay JSON, got %q", text)
		}
	})
	t.Run("isError", func(t *testing.T) {
		srv := newProxyToonServer(t, nil)
		defer srv.Close()
		conn := fakeConn("fail")
		conn.Responses["tools/call"] = json.RawMessage(
			`{"content":[{"type":"text","text":"{\"code\":7}"}],"isError":true}`,
		)
		addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

		text := toolResultText(t, serveProxy(t, srv, callTool("svc__fail", map[string]any{})))
		if text != `{"code":7}` {
			t.Errorf("isError result must stay JSON, got %q", text)
		}
	})
}

func TestProxy_ToonFormat_PerToolOverrideOnlyAffectsThatTool(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("toon_tool", "json_tool")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "toon_tool",
		"projection": map[string]any{"format": "toon"},
	}))

	toonText := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__toon_tool", map[string]any{}))))
	jsonText := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__json_tool", map[string]any{}))))
	if len(toonText) != 1 || strings.HasPrefix(toonText[0], "{") {
		t.Errorf("configured tool must be TOON, got %q", toonText)
	}
	if len(jsonText) != 1 || !strings.HasPrefix(jsonText[0], "{") {
		t.Errorf("other tool must stay JSON, got %q", jsonText)
	}
}

func TestProxy_ArrayItemExclusion_TrimsEachEntryAndKeepsRecovery(t *testing.T) {
	srv := newTestServer(t, server.Params{Config: config.DefaultConfig()})
	defer srv.Close()
	conn := fakeConn("list_commits")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"[{\"sha\":\"a\",\"author\":{\"login\":\"x\",\"avatar_url\":\"u1\"}},{\"sha\":\"b\",\"author\":{\"login\":\"y\",\"avatar_url\":\"u2\"}}]"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	setProxyExclude(
		setProxyExcludeParams{
			T:          t,
			Server:     srv,
			Tool:       "list_commits",
			Projection: map[string]any{"exclude": []string{"[].author.avatar_url"}},
		},
	)

	texts := contentTexts(proxyResultOf(t, serveProxy(t, srv, callTool("svc__list_commits", map[string]any{}))))
	if len(texts) != 2 || texts[0] != `[{"author":{"login":"x"},"sha":"a"},{"author":{"login":"y"},"sha":"b"}]` {
		t.Fatalf("want trimmed entries plus a notice block, got %q", texts)
	}
	var notice struct {
		Mini struct {
			File     string   `json:"file"`
			Excluded []string `json:"excluded"`
		} `json:"__mini"`
	}
	if err := json.Unmarshal([]byte(texts[1]), &notice); err != nil {
		t.Fatalf("notice block is not JSON: %q", texts[1])
	}
	const path = ".content[0].text | fromjson | .[].author.avatar_url"
	if len(notice.Mini.Excluded) != 1 || notice.Mini.Excluded[0] != path {
		t.Fatalf("excluded = %q, want [%q]", notice.Mini.Excluded, path)
	}
	read := callTool("read", map[string]any{"file": notice.Mini.File, "filter": path})
	if recovered := toolResultText(t, serveProxy(t, srv, read)); !strings.Contains(recovered, "u1") ||
		!strings.Contains(recovered, "u2") {
		t.Errorf("read(%q) = %q, want both original avatar URLs", path, recovered)
	}
}
