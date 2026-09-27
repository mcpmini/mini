package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestMarkAndIsOAuthDetected(t *testing.T) {
	dir := t.TempDir()
	if config.IsOAuthDetected(dir, "myserver") {
		t.Error("expected false before marking")
	}
	if err := config.MarkOAuthDetected(dir, "myserver"); err != nil {
		t.Fatalf("MarkOAuthDetected: %v", err)
	}
	if !config.IsOAuthDetected(dir, "myserver") {
		t.Error("expected true after marking")
	}
	if config.IsOAuthDetected(dir, "otherserver") {
		t.Error("marking one server must not affect another")
	}
}

func TestMarkOAuthDetected_invalidName(t *testing.T) {
	if err := config.MarkOAuthDetected(t.TempDir(), "../escape"); err == nil {
		t.Fatal("expected error for invalid server name")
	}
}

func TestIsOAuthDetected_invalidName(t *testing.T) {
	dir := t.TempDir()
	escaped := config.ServerMetaPath(dir, "../escape")
	if err := os.MkdirAll(filepath.Dir(escaped), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(escaped, []byte(`{"oauth_detected":true}`), 0600); err != nil {
		t.Fatal(err)
	}

	if config.IsOAuthDetected(dir, "../escape") {
		t.Error("an invalid server name must never report as detected, even when its traversed path holds a marker")
	}
}
