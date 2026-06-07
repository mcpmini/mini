//go:build test

package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDaemonLog_rotatesLargeFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	content := bytes.Repeat([]byte("x"), maxDaemonLogBytes+1)
	if err := os.WriteFile(logPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	f, close := openDaemonLog(dir)
	close()

	rotated, err := os.ReadFile(logPath + ".1")
	if err != nil {
		t.Fatalf("daemon.log.1 not found: %v", err)
	}
	if !bytes.Equal(rotated, content) {
		t.Error("daemon.log.1 content does not match original")
	}
	info, err := f.Stat()
	if err != nil {
		info, err = os.Stat(logPath)
		if err != nil {
			t.Fatalf("daemon.log not found: %v", err)
		}
	}
	if info.Size() >= int64(maxDaemonLogBytes) {
		t.Errorf("new daemon.log should be small, got size %d", info.Size())
	}
}

func TestOpenDaemonLog_noRotationBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	if err := os.WriteFile(logPath, []byte("small log"), 0600); err != nil {
		t.Fatal(err)
	}

	f, close := openDaemonLog(dir)
	close()

	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Error("expected daemon.log.1 to not exist for small log file")
	}
	_ = f
}
