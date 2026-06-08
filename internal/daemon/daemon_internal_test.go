//go:build test

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCappedLog_rotatesWhenFull(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	w := OpenCappedLog(logPath)
	defer w.Close()

	// Fill just past the cap, then write one more line to trigger rotation.
	big := strings.Repeat("x", int(maxDaemonLogBytes))
	w.Write([]byte(big))
	w.Write([]byte("after-rotation\n"))

	if _, err := os.Stat(logPath + ".old"); err != nil {
		t.Fatalf("expected daemon.log.old after rotation: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	if string(data) != "after-rotation\n" {
		t.Errorf("expected only post-rotation content, got %q", data)
	}
}

func TestCappedLog_appendsBelowCap(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	w := OpenCappedLog(logPath)
	w.Write([]byte("line1\n"))
	w.Close()

	w = OpenCappedLog(logPath)
	w.Write([]byte("line2\n"))
	w.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	if string(data) != "line1\nline2\n" {
		t.Errorf("expected appended content, got %q", data)
	}
	if _, err := os.Stat(logPath + ".old"); !os.IsNotExist(err) {
		t.Error("expected no .old file for small log")
	}
}
