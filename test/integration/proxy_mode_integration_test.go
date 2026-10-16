//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func proxySetup(t *testing.T, fixtures map[string]string) *mcpClient {
	t.Helper()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, fixtures)
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	return startProxyServer(t, cfg)
}

func TestIntegrationProxyMode_UntrimmedResult_ArrivesAsUpstreamSentIt(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"object": `{"id":1,"name":"widget"}`,
		"array":  `[{"id":1},{"id":2},{"id":3}]`,
		"null":   `null`,
	}
	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := proxySetup(t, map[string]string{"get_item": fixture})
			pr := c.execProxyTool("svc__get_item", map[string]any{}, nil)
			if len(pr.Content) != 1 || pr.Content[0].Text != fixture {
				t.Errorf("expected the upstream text %q as the only block, got %+v", fixture, pr.Content)
			}
		})
	}
}

func TestIntegrationProxyMode_TrimmedArray_CarriesNoticeBlockAndReadRecoversIt(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"list_items": `[{"id":1},{"id":2},{"id":3},{"id":4}]`})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	configtest.WriteProjections(t, cfg, configtest.ProjectionFile{
		ServerName: "svc",
		Tools:      map[string]*config.ProjectionConfig{"list_items": {ArrayLimits: map[string]int{"default": 2}}},
	})
	c := startProxyServer(t, cfg)

	pr := c.execProxyTool("svc__list_items", map[string]any{}, nil)

	if len(pr.Content) != 2 || pr.Content[0].Text != `[{"id":1},{"id":2}]` ||
		!strings.HasPrefix(pr.Content[1].Text, "\n") {
		t.Fatalf("expected the trimmed array plus a trailing notice block, got %+v", pr.Content)
	}
	mini := parseMiniEnv(t, pr.Content[1].Text)
	if len(mini.Truncated) != 1 || mini.Truncated[0].Path != ".content[0].text | fromjson | ." {
		t.Fatalf("unexpected truncated paths: %+v", mini.Truncated)
	}
	got := c.callRead(mini.File, mini.Truncated[0].Path)
	if !strings.Contains(strings.ReplaceAll(got, " ", ""), `[{"id":1},{"id":2},{"id":3},{"id":4}]`) {
		t.Errorf("read did not recover the full array: %s", got)
	}
}

func TestIntegrationProxyMode_ArgsReachUpstream(t *testing.T) {
	t.Parallel()
	c := proxySetup(t, map[string]string{"echo": `{"echo":"default","other":"unchanged"}`})
	pr := c.execProxyTool("svc__echo", map[string]any{"echo": "from-agent"}, nil)
	m, ok := pr.textJSON(t, 0).(map[string]any)
	if !ok || m["echo"] != "from-agent" || m["other"] != "unchanged" {
		t.Errorf("expected args.echo to overlay the fixture value, got %v", pr.Content)
	}
}

// Exclusion → __mini population and read() recovery for proxy mode are already
// covered by TestIntegrationProjection_readRecoversProjectedData (projection_integration_test.go).
// This test covers the one thing that table doesn't: __mini.projection:"raw"
// bypassing exclusion entirely.
func TestIntegrationProxyMode_RawProjectionBypassesExclusion(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden","name":"Alice"}`})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	configtest.WriteProjections(t, cfg, configtest.ProjectionFile{
		ServerName: "svc",
		Tools: map[string]*config.ProjectionConfig{
			"get_item": {
				Exclude: []string{"secret"},
			},
		},
	})
	c := startProxyServer(t, cfg)

	pr := c.execProxyTool("svc__get_item", map[string]any{}, map[string]any{"projection": "raw"})
	m, ok := pr.textJSON(t, 0).(map[string]any)
	if !ok || m["secret"] != "hidden" || m["__mini"] != nil {
		t.Errorf("expected raw projection to bypass exclusion, got %v", pr.Content)
	}
}

func TestIntegrationProxyMode_LegacyFlatCall_Rejected(t *testing.T) {
	t.Parallel()
	c := proxySetup(t, map[string]string{"get_item": `{"id":1}`})
	text, isErr := c.execProxyToolRaw("svc__get_item", map[string]any{"id": 1})
	if !isErr {
		t.Fatalf("expected legacy flat call to be rejected, got success: %s", text)
	}
	if !strings.Contains(text, `"args"`) {
		t.Errorf("expected actionable error mentioning \"args\", got: %s", text)
	}
}
