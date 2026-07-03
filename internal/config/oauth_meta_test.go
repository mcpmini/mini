package config_test

import (
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
	if config.IsOAuthDetected(t.TempDir(), "../escape") {
		t.Error("an invalid server name must never report as detected")
	}
}
