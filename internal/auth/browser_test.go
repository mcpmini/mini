//go:build test

package auth_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
)

func TestOpenBrowser_withCmd_noError(t *testing.T) {
	if err := auth.OpenBrowser("true", "http://example.com"); err != nil {
		t.Errorf("OpenBrowser with 'true': %v", err)
	}
}

// TestOpenBrowser_urlPassedAsArg verifies the URL is passed as a separate
// shell argument, not interpolated into the command string. This matters when
// the URL contains shell metacharacters (e.g. &, $, ()).
func TestOpenBrowser_urlPassedAsArg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: shell quoting behavior")
	}
	dir := t.TempDir()
	outFile := filepath.Join(dir, "captured.txt")
	script := filepath.Join(dir, "capture.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$1\" > "+outFile+"\n"), 0700) //nolint:errcheck

	url := "http://example.com?a=1&b=$(echo injected)&c=hello world"
	if err := auth.OpenBrowser(script, url); err != nil {
		t.Fatalf("OpenBrowser: %v", err)
	}

	for range 200 {
		if data, err := os.ReadFile(outFile); err == nil {
			if string(data) != url {
				t.Errorf("captured URL = %q, want %q", string(data), url)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("timed out waiting for browser command to write output")
}
