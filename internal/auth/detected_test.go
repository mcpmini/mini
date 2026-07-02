//go:build test

package auth_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/auth"
)

func TestMarkAndIsOAuthDetected(t *testing.T) {
	dir := t.TempDir()
	if auth.IsOAuthDetected(dir, "myserver") {
		t.Fatal("expected no marker before MarkOAuthDetected")
	}
	if err := auth.MarkOAuthDetected(dir, "myserver"); err != nil {
		t.Fatalf("MarkOAuthDetected: %v", err)
	}
	if !auth.IsOAuthDetected(dir, "myserver") {
		t.Error("expected marker after MarkOAuthDetected")
	}
	if auth.IsOAuthDetected(dir, "otherserver") {
		t.Error("marker should be scoped to the named server only")
	}
}

func TestMarkOAuthDetected_invalidName(t *testing.T) {
	if err := auth.MarkOAuthDetected(t.TempDir(), "bad name!"); err == nil {
		t.Error("expected error for invalid server name")
	}
}
