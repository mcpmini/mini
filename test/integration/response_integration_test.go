//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestIntegrationResponse_inlineSmallResponse(t *testing.T) {
	e := quickServer(t, map[string]string{"get_item": `{"id":1,"name":"small"}`}).execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Fatalf("expected ok=true, got: %+v", e)
	}
	if e.File != nil {
		t.Errorf("small response should be inline, got file=%q", *e.File)
	}
}

func TestIntegrationResponse_projectedResponseWrittenToRawFile(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, fakeServerParams{
		ServerName: "svc",
		Fixtures:   mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden"}`}),
	})
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.ResponseDir = respDir
	configtest.WriteConfig(t, cfg, fixtureConfig)

	configtest.WriteProjections(t, cfg, configtest.ProjectionFile{
		ServerName: "svc",
		Tools: map[string]*config.ProjectionConfig{
			"get_item": {
				Exclude: []string{"secret"},
			},
		},
	})

	e := startServer(t, cfg).execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Fatal("projected response should have written a raw file")
	}
	if _, err := os.Stat(filepath.Join(respDir, *e.File+".json")); err != nil {
		t.Errorf("response file %q should exist: %v", *e.File, err)
	}
}

func TestIntegrationResponse_responseFileIsValidJSON(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, fakeServerParams{
		ServerName: "svc",
		Fixtures:   mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden"}`}),
	})
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.ResponseDir = respDir
	configtest.WriteConfig(t, cfg, fixtureConfig)

	configtest.WriteProjections(t, cfg, configtest.ProjectionFile{
		ServerName: "svc",
		Tools: map[string]*config.ProjectionConfig{
			"get_item": {
				Exclude: []string{"secret"},
			},
		},
	})

	e := startServer(t, cfg).execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	data := testutil.ReadFile(t, filepath.Join(respDir, *e.File+".json"))
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("response file is not valid JSON: %v", err)
	}
}

func TestIntegrationResponse_okFalseOnUpstreamError(t *testing.T) {
	e := quickServer(t, map[string]string{
		"failing_tool": `{"__mcp_error": "service unavailable"}`,
	}).execEnvelope("svc", "failing_tool", nil)

	if e.Error == "" {
		t.Error("upstream error should produce ok=false in envelope")
	}
}

func TestIntegrationResponse_execOkField(t *testing.T) {
	e := quickServer(t, map[string]string{"get_item": `{"id":1}`}).execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Errorf("expected ok=true on successful call, got: %+v", e)
	}
}
