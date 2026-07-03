//go:build integration

package integration_test

import (
	"strings"
	"testing"
)

func proxySetup(t *testing.T, fixtures map[string]string) *mcpClient {
	t.Helper()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, fixtures)
	writeFakeServer(t, cfg, "svc", dir)
	return startProxyServer(t, cfg)
}

func TestProxyMode_BasicCall_WrapsInData(t *testing.T) {
	c := proxySetup(t, map[string]string{"get_item": `{"id":1,"name":"widget"}`})
	pr := c.execProxyTool("svc__get_item", map[string]any{}, nil)
	if pr.Mini != nil {
		t.Errorf("expected no __mini for unaltered response, got %+v", pr.Mini)
	}
	m, ok := pr.Data.(map[string]any)
	if !ok || m["id"] != float64(1) || m["name"] != "widget" {
		t.Errorf("expected data to contain upstream fields, got %v", pr.Data)
	}
}

func TestProxyMode_ArrayRoot_WrapsInData(t *testing.T) {
	c := proxySetup(t, map[string]string{"list_items": `[{"id":1},{"id":2},{"id":3}]`})
	pr := c.execProxyTool("svc__list_items", map[string]any{}, nil)
	items, ok := pr.Data.([]any)
	if !ok || len(items) != 3 {
		t.Errorf("expected array root preserved in data, got %v", pr.Data)
	}
}

func TestProxyMode_NullResult_WrapsAsDataNull(t *testing.T) {
	c := proxySetup(t, map[string]string{"get_nothing": `null`})
	raw, isErr := c.execProxyToolAllowError("svc__get_nothing", map[string]any{}, nil)
	if isErr {
		t.Fatalf("expected success, got error: %s", raw)
	}
	if strings.TrimSpace(raw) != `{"data":null}` {
		t.Errorf(`expected exactly {"data":null}, got: %s`, raw)
	}
}

func TestProxyMode_ArgsReachUpstream(t *testing.T) {
	c := proxySetup(t, map[string]string{"echo": `{"echo":"default","other":"unchanged"}`})
	pr := c.execProxyTool("svc__echo", map[string]any{"echo": "from-agent"}, nil)
	m, ok := pr.Data.(map[string]any)
	if !ok || m["echo"] != "from-agent" || m["other"] != "unchanged" {
		t.Errorf("expected args.echo to overlay the fixture value, got %v", pr.Data)
	}
}

// Exclusion → __mini population and read() recovery for proxy mode are already
// covered by TestProjection_readRecoversProjectedData (projection_test.go).
// This test covers the one thing that table doesn't: __mini.projection:"raw"
// bypassing exclusion entirely.
func TestProxyMode_RawProjectionBypassesExclusion(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden","name":"Alice"}`})
	writeFakeServer(t, cfg, "svc", dir)
	writeProjection(t, cfg, "svc", "get_item:\n  exclude: [secret]\n")
	c := startProxyServer(t, cfg)

	pr := c.execProxyTool("svc__get_item", map[string]any{}, map[string]any{"projection": "raw"})
	if pr.Mini != nil {
		t.Errorf("expected no __mini metadata under raw projection, got %+v", pr.Mini)
	}
	m, ok := pr.Data.(map[string]any)
	if !ok || m["secret"] != "hidden" {
		t.Errorf("expected raw projection to bypass exclusion, got %v", pr.Data)
	}
}

func TestProxyMode_LegacyFlatCall_Rejected(t *testing.T) {
	c := proxySetup(t, map[string]string{"get_item": `{"id":1}`})
	text, isErr := c.execProxyToolRaw("svc__get_item", map[string]any{"id": 1})
	if !isErr {
		t.Fatalf("expected legacy flat call to be rejected, got success: %s", text)
	}
	if !strings.Contains(text, `"args"`) {
		t.Errorf("expected actionable error mentioning \"args\", got: %s", text)
	}
}
