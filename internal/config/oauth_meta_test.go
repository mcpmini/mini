package config_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
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
	testutil.WriteFile(t, escaped, `{"oauth_detected":true}`)

	if config.IsOAuthDetected(dir, "../escape") {
		t.Error("an invalid server name must never report as detected, even when its traversed path holds a marker")
	}
}

func TestIsOAuthDetected_corruptMetadataDoesNotTrustPartialFields(t *testing.T) {
	dir := t.TempDir()
	path := config.ServerMetaPath(dir, "myserver")
	testutil.WriteFile(t, path, `{"oauth_detected":true,"oauth_detected":0}`)

	if config.IsOAuthDetected(dir, "myserver") {
		t.Fatal("corrupt metadata must not preserve partially decoded OAuth state")
	}
}
