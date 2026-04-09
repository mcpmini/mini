//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"testing"
)

func TestResponse_inlineSmallResponse(t *testing.T) {
	e := quickServer(t, map[string]string{"get_item": `{"id":1,"name":"small"}`}).execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Fatalf("expected ok=true, got: %+v", e)
	}
	if e.File != nil {
		t.Errorf("small response should be inline, got file=%q", *e.File)
	}
}

func TestResponse_largeResponseWrittenToFile(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	writeConfig(t, cfg, "inline_threshold: 1\nresponse_dir: "+t.TempDir()+"\n")

	e := startServer(t, cfg).execEnvelope("github", "list_pull_requests", nil)
	if e.File == nil {
		t.Fatal("large response should have written a file")
	}
	if _, err := os.Stat(*e.File); err != nil {
		t.Errorf("response file %q should exist: %v", *e.File, err)
	}
}

func TestResponse_responseFileIsValidJSON(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	writeConfig(t, cfg, "inline_threshold: 1\nresponse_dir: "+t.TempDir()+"\n")

	e := startServer(t, cfg).execEnvelope("github", "list_pull_requests", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	data, err := os.ReadFile(*e.File)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("response file is not valid JSON: %v", err)
	}
}

func TestResponse_okFalseOnUpstreamError(t *testing.T) {
	e := quickServer(t, map[string]string{
		"failing_tool": `{"__mcp_error": "service unavailable"}`,
	}).execEnvelope("svc", "failing_tool", nil)

	if e.Error == "" {
		t.Error("upstream error should produce ok=false in envelope")
	}
}

func TestResponse_execOkField(t *testing.T) {
	e := quickServer(t, map[string]string{"get_item": `{"id":1}`}).execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Errorf("expected ok=true on successful call, got: %+v", e)
	}
}
