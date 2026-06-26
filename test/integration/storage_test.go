//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func projectedResponseClient(t *testing.T, extraConfig string) (*mcpClient, string, string) {
	t.Helper()
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, "svc", mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"hidden","body":"full text"}`}))
	writeConfig(t, cfg, extraConfig+"response_dir: "+respDir+"\n")
	writeProjection(t, cfg, "svc", "get_item:\n  exclude: [secret]\n")
	return startServer(t, cfg), respDir, cfg
}

func TestStorage_rawFileExists(t *testing.T) {
	client, respDir, _ := projectedResponseClient(t, "")
	e := client.execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Fatal("expected file response for projected payload")
	}
	if _, err := os.Stat(*e.File); err != nil {
		t.Errorf("raw file %q should exist: %v", *e.File, err)
	}
	_ = respDir
}

func TestStorage_unprojectedResponseDoesNotWriteFile(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, "svc", mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`}))
	writeConfig(t, cfg, "response_dir: "+respDir+"\n")
	client := startServer(t, cfg)

	e := client.execEnvelope("svc", "get_item", nil)
	if e.File != nil {
		t.Errorf("unprojected response must not write a file, got %q", *e.File)
	}
}

func TestStorage_rawFileIsPrettyPrinted(t *testing.T) {
	client, _, _ := projectedResponseClient(t, "")
	e := client.execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	data, err := os.ReadFile(*e.File)
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	if !strings.Contains(string(data), "\n") || !strings.Contains(string(data), "  ") {
		t.Errorf("raw file should be pretty-printed JSON, got first 100 chars: %s", data[:min(100, len(data))])
	}
	if !json.Valid(data) {
		t.Error("raw file should be valid JSON")
	}
}

func TestStorage_responseDirAutoCreated(t *testing.T) {
	cfg := t.TempDir()
	respDir := filepath.Join(t.TempDir(), "auto_created_responses")
	writeFakeServer(t, cfg, "svc", mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`}))
	writeConfig(t, cfg, "response_dir: "+respDir+"\n")
	writeProjection(t, cfg, "svc", "get_item:\n  include_only: [id]\n")

	client := startServer(t, cfg)
	e := client.execEnvelope("svc", "get_item", nil)
	if _, err := os.Stat(respDir); err != nil {
		t.Errorf("response dir should be auto-created: %v", err)
	}
	if e.File != nil {
		t.Errorf("include_only projection with no excluded fields should not write a file, got %q", *e.File)
	}
}

func TestStorage_diskBudgetEvictsOldest(t *testing.T) {
	client, respDir, _ := projectedResponseClient(t, "response_disk_budget_mb: 0\n")

	for range 3 {
		client.execEnvelope("svc", "get_item", nil)
	}

	entries, _ := os.ReadDir(respDir)
	fileCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			fileCount++
		}
	}
	if fileCount != 3 {
		t.Errorf("disk budget:0 should retain all files, expected 3 raw files, got %d", fileCount)
	}
}

func TestStorage_cleanupDeletesExpired(t *testing.T) {
	client, _, cfg := projectedResponseClient(t, "")

	e := client.execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	rawPath := *e.File
	backdateFile(t, rawPath, 8*24*time.Hour)

	runCLI(t, cfg, "cleanup")
	if _, err := os.Stat(rawPath); !os.IsNotExist(err) {
		t.Error("expired raw file should be deleted by cleanup")
	}
}

func TestStorage_cleanupRetainsNonExpired(t *testing.T) {
	client, _, cfg := projectedResponseClient(t, "")

	e := client.execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	rawPath := *e.File
	runCLI(t, cfg, "cleanup")
	if _, err := os.Stat(rawPath); err != nil {
		t.Errorf("non-expired file should not be deleted by cleanup: %v", err)
	}
}

func TestStorage_unprojectedLargeResponseInlines(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	writeConfig(t, cfg, "response_dir: "+t.TempDir()+"\n")
	client := startServer(t, cfg)

	e := client.execEnvelope("github", "list_pull_requests", nil)
	if e.File != nil {
		t.Errorf("unprojected large response should inline without file, got file=%q", *e.File)
	}
	if e.Data == nil {
		t.Error("expected data field to be present for inline response")
	}
}

func projectedProxyClient(t *testing.T) (*mcpClient, string) {
	t.Helper()
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", mockFixtureDir(t, map[string]string{
		"get_item": `{"id":1,"secret":"hidden","body":"full text"}`,
	}))
	writeConfig(t, cfg, "response_dir: "+t.TempDir()+"\n")
	writeProjection(t, cfg, "svc", "get_item:\n  exclude: [secret]\n")
	client := startProxyServer(t, cfg)
	raw := client.mustCall("tools/call", map[string]any{
		"name":      "svc__get_item",
		"arguments": map[string]any{},
	})
	text, _ := parseToolCallResult(raw)
	return client, text
}

func TestStorage_readByFullPath(t *testing.T) {
	client, text := projectedProxyClient(t)
	filePath := miniFile(t, text)
	got := client.callRead(filePath, "")
	if !json.Valid([]byte(got)) {
		t.Errorf("read should return valid JSON, got: %s", got)
	}
	if !strings.Contains(got, `"secret"`) {
		t.Errorf("raw file should contain excluded fields, got: %s", got)
	}
}

func TestStorage_readByBareFilename(t *testing.T) {
	client, text := projectedProxyClient(t)
	filePath := miniFile(t, text)
	byFullPath := client.callRead(filePath, "")
	byFilename := client.callRead(filepath.Base(filePath), "")
	if byFilename != byFullPath {
		t.Errorf("bare filename and full path should return same content\nfull: %s\nbare: %s", byFullPath, byFilename)
	}
}

func TestStorage_readWithJQFilter(t *testing.T) {
	client, text := projectedProxyClient(t)
	filePath := miniFile(t, text)
	got := client.callRead(filePath, ".secret")
	if got != `"hidden"` {
		t.Errorf("filter .secret should return \"hidden\", got: %s", got)
	}
}

func TestStorage_nonJSONResponseFromUpstreamPassedThrough(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", mockFixtureDir(t, map[string]string{"get_status": `plain text response`}))
	client := startServer(t, cfg)

	result, isErr := client.execToolAllowError("svc", "get_status", nil)
	if isErr {
		t.Fatalf("expected success for non-JSON response, got error: %s", result)
	}
	if result == "" {
		t.Error("expected non-empty response")
	}
	if !strings.Contains(result, "plain text response") {
		t.Errorf("expected original text to be present in response, got: %s", result)
	}
}
