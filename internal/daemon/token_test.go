package daemon_test

import (
	"os"
	"testing"

	"github.com/mcpmini/mini/internal/daemon"
)

func TestWriteToken_replacesLooseFileWith0600(t *testing.T) {
	configDir := t.TempDir()
	stale := daemon.TokenFile(configDir)
	if err := os.WriteFile(stale, []byte("old"), 0644); err != nil {
		t.Fatalf("seed stale token: %v", err)
	}

	token, err := daemon.WriteToken(configDir)
	if err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	info, err := os.Stat(stale)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("token mode = %o, want 0600 (must tighten the stale 0644 file)", perm)
	}

	got, err := daemon.ReadToken(configDir)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != token {
		t.Errorf("round-trip mismatch: read %q, wrote %q", got, token)
	}
	if got == "old" {
		t.Error("stale token was not replaced")
	}
}

func TestWriteToken_returnsDistinctTokens(t *testing.T) {
	configDir := t.TempDir()
	first, err := daemon.WriteToken(configDir)
	if err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	second, err := daemon.WriteToken(configDir)
	if err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	if first == second {
		t.Error("expected a freshly minted token on each call")
	}
}
