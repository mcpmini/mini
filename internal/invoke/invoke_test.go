//go:build test

package invoke_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

func fakeConn(method, body string) *transport.FakeConnection {
	return &transport.FakeConnection{
		Responses: map[string]json.RawMessage{
			method: json.RawMessage(body),
		},
	}
}

func toolResponse(text string, isError bool) string {
	errStr := "false"
	if isError {
		errStr = "true"
	}
	b, _ := json.Marshal(text)
	return `{"content":[{"type":"text","text":` + string(b) + `}],"isError":` + errStr + `}`
}

func noopBuilder(t *testing.T) *response.Builder {
	t.Helper()
	store, err := response.NewStore(response.StoreConfig{
		Dir:             t.TempDir(),
		TTL:             time.Hour,
		CleanupInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return response.NewBuilder(store)
}

func noopDefaults() *projection.Defaults {
	return &projection.Defaults{}
}

// ExtractContent

func TestExtractContent_JSONText(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1}"}],"isError":false}`)
	out, err := invoke.ExtractContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"id":1}` {
		t.Errorf("got %s", out)
	}
}

func TestExtractContent_PlainText(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"hello world"}],"isError":false}`)
	out, err := invoke.ExtractContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"hello world"` {
		t.Errorf("got %s", out)
	}
}

func TestExtractContent_MultiContent(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"foo"},{"type":"text","text":"bar"}],"isError":false}`)
	out, err := invoke.ExtractContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"foo\nbar"` {
		t.Errorf("got %s", out)
	}
}

func TestExtractContent_IsError(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"boom"}],"isError":true}`)
	_, err := invoke.ExtractContent(raw)
	if err == nil || !contains(err.Error(), "boom") {
		t.Errorf("expected error containing 'boom', got %v", err)
	}
}

func TestExtractContent_Malformed(t *testing.T) {
	_, err := invoke.ExtractContent(json.RawMessage(`not json`))
	if err == nil {
		t.Error("expected error for malformed response")
	}
}

// TestExtractContent_StructuredContent verifies that structuredContent (added in
// MCP spec 2025-06-18) is used when a server omits the text content fallback.
// Spec: servers SHOULD include a text representation for backwards compatibility,
// but not all do.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-06-18/server/tools.mdx#structured-content
func TestExtractContent_StructuredContentFallback(t *testing.T) {
	raw := json.RawMessage(`{"content":[],"structuredContent":{"temp":72,"unit":"F"}}`)
	out, err := invoke.ExtractContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"temp":72,"unit":"F"}` {
		t.Errorf("got %s, want structuredContent passthrough", out)
	}
}

func TestExtractContent_StructuredContentPreferText(t *testing.T) {
	// When both text and structuredContent are present, text wins (backwards compat).
	raw := json.RawMessage(`{"content":[{"type":"text","text":"{\"temp\":72}"}],"structuredContent":{"temp":72}}`)
	out, err := invoke.ExtractContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"temp":72}` {
		t.Errorf("got %s, want text-content value", out)
	}
}

// InvokeRaw

func TestInvokeRaw_BasicCall(t *testing.T) {
	conn := fakeConn("tools/call", toolResponse(`{"id":42}`, false))
	raw, _, err := invoke.InvokeRaw(context.Background(), conn, "my_tool", map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"id":42}` {
		t.Errorf("got %s", raw)
	}
}

func TestInvokeRaw_ToolError(t *testing.T) {
	conn := fakeConn("tools/call", toolResponse("something failed", true))
	_, _, err := invoke.InvokeRaw(context.Background(), conn, "my_tool", nil)
	if err == nil {
		t.Error("expected error")
	}
}

func TestInvokeRaw_ConnError(t *testing.T) {
	conn := &transport.FakeConnection{Err: errors.New("network down")}
	_, _, err := invoke.InvokeRaw(context.Background(), conn, "my_tool", nil)
	if err == nil || !contains(err.Error(), "network down") {
		t.Errorf("expected conn error, got %v", err)
	}
}

func TestInvokeRaw_NilParams(t *testing.T) {
	conn := fakeConn("tools/call", toolResponse(`"ok"`, false))
	raw, _, err := invoke.InvokeRaw(context.Background(), conn, "my_tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"ok"` {
		t.Errorf("got %s", raw)
	}
}

// BuildEnvelope

func TestBuildEnvelope_NoProjection(t *testing.T) {
	raw := json.RawMessage(`{"items":[1,2,3]}`)
	env, _, err := invoke.BuildEnvelope(invoke.BuildEnvelopeParams{
		Server:   "svc",
		Tool:     "list",
		Raw:      raw,
		ProjCfg:  nil,
		ProjDefs: noopDefaults(),
		Builder:  noopBuilder(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Error != "" {
		t.Errorf("expected success, got error: %s", env.Error)
	}
	if env.Data == nil {
		t.Error("expected data")
	}
}

func TestBuildEnvelope_WithProjection(t *testing.T) {
	raw := json.RawMessage(`{"id":1,"secret":"hidden","name":"Alice"}`)
	projCfg := &config.ProjectionConfig{Exclude: []string{"secret"}}
	env, _, err := invoke.BuildEnvelope(invoke.BuildEnvelopeParams{
		Server:   "svc",
		Tool:     "get",
		Raw:      raw,
		ProjCfg:  projCfg,
		ProjDefs: noopDefaults(),
		Builder:  noopBuilder(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Error != "" {
		t.Errorf("expected success, got error: %s", env.Error)
	}
	if !containsString(env.Excluded, ".secret") {
		t.Errorf("expected '.secret' in excluded, got %v", env.Excluded)
	}
	b, _ := json.Marshal(env.Data)
	if contains(string(b), "hidden") {
		t.Errorf("secret value should be excluded, got: %s", b)
	}
}

func TestBuildEnvelope_TruncationRecorded(t *testing.T) {
	longBody := strings.Repeat("x", 500)
	raw := json.RawMessage(`{"id":1,"body":"` + longBody + `"}`)
	projCfg := &config.ProjectionConfig{StringLimits: map[string]int{"body": 50}}
	env, _, err := invoke.BuildEnvelope(invoke.BuildEnvelopeParams{
		Server:   "svc",
		Tool:     "get",
		Raw:      raw,
		ProjCfg:  projCfg,
		ProjDefs: noopDefaults(),
		Builder:  noopBuilder(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Truncated) == 0 || env.Truncated[0].Chars == 0 {
		t.Errorf("expected truncated[0].Chars > 0, got %v", env.Truncated)
	}
	b, _ := json.Marshal(env.Data)
	if contains(string(b), longBody) {
		t.Errorf("body should be truncated in data, got full value")
	}
}

// Invoke (end-to-end)

func TestInvoke_HappyPath(t *testing.T) {
	conn := fakeConn("tools/call", toolResponse(`{"result":"ok"}`, false))
	result, err := invoke.Invoke(context.Background(), invoke.InvokeParams{
		Server:   "svc",
		Tool:     "do_thing",
		Params:   map[string]any{"input": "x"},
		Conn:     conn,
		ProjCfg:  nil,
		ProjDefs: noopDefaults(),
		Builder:  noopBuilder(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Envelope.Error != "" {
		t.Errorf("expected success, got error: %s", result.Envelope.Error)
	}
}

func TestInvoke_ToolError(t *testing.T) {
	conn := fakeConn("tools/call", toolResponse("bad input", true))
	_, err := invoke.Invoke(context.Background(), invoke.InvokeParams{
		Conn:     conn,
		Tool:     "do_thing",
		ProjDefs: noopDefaults(),
		Builder:  noopBuilder(t),
	})
	if err == nil || !contains(err.Error(), "bad input") {
		t.Errorf("expected tool error, got %v", err)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
