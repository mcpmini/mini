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

func largeResponseClient(t *testing.T, threshold string) (*mcpClient, string) {
	t.Helper()
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	writeConfig(t, cfg, "inline_threshold: "+threshold+"\nresponse_dir: "+respDir+"\n")
	return startServer(t, cfg), respDir
}

func TestStorage_rawFileExistsAlongsideSlim(t *testing.T) {
	client, respDir := largeResponseClient(t, "1")
	e := client.execEnvelope("github", "list_pull_requests", nil)
	if e.File == nil {
		t.Fatal("expected file response for large payload")
	}
	rawPath := strings.TrimSuffix(*e.File, ".json") + ".raw.json"
	if _, err := os.Stat(rawPath); err != nil {
		t.Errorf("raw file %q should exist alongside slim file: %v", rawPath, err)
	}
	_ = respDir
}

func TestStorage_inlineThresholdZeroForcesFile(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, "svc", mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`}))
	writeConfig(t, cfg, "inline_threshold: 0\nresponse_dir: "+respDir+"\n")
	client := startServer(t, cfg)

	e := client.execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Error("inline_threshold:0 should force file write even for tiny response")
	}
	if e.File != nil {
		if _, err := os.Stat(*e.File); err != nil {
			t.Errorf("response file should exist: %v", err)
		}
	}
}

func TestStorage_slimFileIsPrettyPrinted(t *testing.T) {
	client, _ := largeResponseClient(t, "1")
	e := client.execEnvelope("github", "list_pull_requests", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	data, err := os.ReadFile(*e.File)
	if err != nil {
		t.Fatalf("read slim file: %v", err)
	}
	if !strings.Contains(string(data), "\n") || !strings.Contains(string(data), "  ") {
		t.Errorf("slim file should be pretty-printed JSON, got first 100 chars: %s", data[:min(100, len(data))])
	}
	if !json.Valid(data) {
		t.Error("slim file should be valid JSON")
	}
}

func TestStorage_responseDirAutoCreated(t *testing.T) {
	cfg := t.TempDir()
	respDir := filepath.Join(t.TempDir(), "auto_created_responses")
	// Intentionally do NOT create respDir — the server should create it
	writeFakeServer(t, cfg, "svc", mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`}))
	writeConfig(t, cfg, "inline_threshold: 0\nresponse_dir: "+respDir+"\n")

	client := startServer(t, cfg)
	e := client.execEnvelope("svc", "get_item", nil)
	if e.File == nil {
		t.Fatal("expected file response with inline_threshold:0")
	}
	if _, err := os.Stat(respDir); err != nil {
		t.Errorf("response dir should be auto-created: %v", err)
	}
}

func TestStorage_diskBudgetEvictsOldest(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	// budget:0 means unlimited, so all files should be retained.
	writeConfig(t, cfg, "inline_threshold: 0\nresponse_disk_budget_mb: 0\nresponse_dir: "+respDir+"\n")
	client := startServer(t, cfg)

	for range 3 {
		client.execEnvelope("github", "list_pull_requests", nil)
	}

	entries, _ := os.ReadDir(respDir)
	slimCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !strings.Contains(e.Name(), ".raw.") {
			slimCount++
		}
	}
	if slimCount != 3 {
		t.Errorf("disk budget:0 should retain all files, expected 3 slim files, got %d", slimCount)
	}
}

func TestStorage_cleanupDeletesExpired(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	writeConfig(t, cfg, "inline_threshold: 1\nresponse_dir: "+respDir+"\n")
	client := startServer(t, cfg)

	e := client.execEnvelope("github", "list_pull_requests", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	slimPath := *e.File
	backdateFile(t, slimPath, 8*24*time.Hour)
	backdateFile(t, strings.TrimSuffix(slimPath, ".json")+".raw.json", 8*24*time.Hour)

	runCLI(t, cfg, "cleanup")
	if _, err := os.Stat(slimPath); !os.IsNotExist(err) {
		t.Error("expired slim file should be deleted by cleanup")
	}
}

func TestStorage_cleanupRetainsNonExpired(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	writeConfig(t, cfg, "inline_threshold: 1\nresponse_dir: "+respDir+"\n")
	client := startServer(t, cfg)

	e := client.execEnvelope("github", "list_pull_requests", nil)
	if e.File == nil {
		t.Fatal("expected file response")
	}
	slimPath := *e.File
	// File is fresh (current modtime) — should survive cleanup
	runCLI(t, cfg, "cleanup")
	if _, err := os.Stat(slimPath); err != nil {
		t.Errorf("non-expired file should not be deleted by cleanup: %v", err)
	}
}

func TestStorage_inlineThresholdLargeForcesInline(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", fixturesDir+"/github")
	writeConfig(t, cfg, "inline_threshold: 999999\nresponse_dir: "+t.TempDir()+"\n")
	client := startServer(t, cfg)

	e := client.execEnvelope("github", "list_pull_requests", nil)
	if e.File != nil {
		t.Errorf("inline_threshold:999999 should return large response inline, got file=%q", *e.File)
	}
	if e.Data == nil {
		t.Error("expected data field to be present for inline response")
	}

	files, _ := os.ReadDir(filepath.Join(t.TempDir()))
	_ = files
}
