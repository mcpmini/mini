//go:build test

package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDaemonLog_truncatesLargeFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	content := bytes.Repeat([]byte("x"), maxDaemonLogBytes+1)
	if err := os.WriteFile(logPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	f, close := openDaemonLog(dir)
	close()

	info, err := f.Stat()
	if err != nil {
		info, err = os.Stat(logPath)
		if err != nil {
			t.Fatalf("daemon.log not found: %v", err)
		}
	}
	if info.Size() != 0 {
		t.Errorf("expected daemon.log truncated to 0, got %d", info.Size())
	}
	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Error("expected no daemon.log.1 with truncate approach")
	}
}

func TestOpenDaemonLog_appendsBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	if err := os.WriteFile(logPath, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}

	f, close := openDaemonLog(dir)
	close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("existing")) {
		t.Error("expected existing content preserved for small log file")
	}
	_ = f
}
