//go:build test

package daemon

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const testCap = 100 // small cap so tests don't need to allocate 10MB

func TestCappedLog_appendsBelowCap(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	w := openCappedLog(logPath, testCap)
	w.Write([]byte("line1\n"))
	w.Close()

	w = openCappedLog(logPath, testCap)
	w.Write([]byte("line2\n"))
	w.Close()

	data := mustReadFile(t, logPath)
	if string(data) != "line1\nline2\n" {
		t.Errorf("expected appended lines, got %q", data)
	}
	if _, err := os.Stat(logPath + ".old"); !os.IsNotExist(err) {
		t.Error("expected no .old file for small log")
	}
}

func TestCappedLog_rotatesWhenFull(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	w := openCappedLog(logPath, testCap)
	w.Write(bytes.Repeat([]byte("x"), testCap))
	w.Write([]byte("after-rotation\n"))
	w.Close()

	if _, err := os.Stat(logPath + ".old"); err != nil {
		t.Fatalf("expected .old file after rotation: %v", err)
	}
	data := mustReadFile(t, logPath)
	if string(data) != "after-rotation\n" {
		t.Errorf("expected only post-rotation content, got %q", data)
	}
}

func TestCappedLog_secondRotationOverwritesOld(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	// First rotation: a's go to .old, "first\n" goes to the fresh file.
	w := openCappedLog(logPath, testCap)
	w.Write(bytes.Repeat([]byte("a"), testCap))
	w.Write([]byte("first\n"))
	w.Close()

	if old := mustReadFile(t, logPath+".old"); !bytes.Equal(old, bytes.Repeat([]byte("a"), testCap)) {
		t.Fatalf("after first rotation: .old should be a's, got %q", old)
	}

	// Second rotation: writing b's triggers rotation; .old becomes "first\n", overwriting the a's.
	w = openCappedLog(logPath, testCap)
	w.Write(bytes.Repeat([]byte("b"), testCap))
	w.Close()

	old := mustReadFile(t, logPath+".old")
	if string(old) != "first\n" {
		t.Errorf(".old should be 'first\\n' after second rotation, got %q", old)
	}
	cur := mustReadFile(t, logPath)
	if !bytes.Equal(cur, bytes.Repeat([]byte("b"), testCap)) {
		t.Errorf("current log should be b's after second rotation, got %q", cur)
	}
	// Only two files total: daemon.log and daemon.log.old — no accumulation.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected exactly 2 log files, got %v", names)
	}
}

func TestCappedLog_initializesWrittenFromExistingFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	// Write near the cap, then close (simulates a previous daemon run).
	existing := bytes.Repeat([]byte("x"), testCap-5)
	if err := os.WriteFile(logPath, existing, 0600); err != nil {
		t.Fatal(err)
	}

	// Reopen and write enough to push past the cap — should rotate.
	w := openCappedLog(logPath, testCap)
	w.Write([]byte("overflow\n"))
	w.Close()

	if _, err := os.Stat(logPath + ".old"); err != nil {
		t.Fatalf("expected rotation after reopen near cap: %v", err)
	}
}

func TestCappedLog_exactBoundary(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	w := openCappedLog(logPath, testCap)
	// Write exactly cap-1 bytes: should not rotate.
	w.Write(bytes.Repeat([]byte("x"), testCap-1))
	w.Close()

	if _, err := os.Stat(logPath + ".old"); !os.IsNotExist(err) {
		t.Error("should not rotate when written == cap-1")
	}

	// One more byte tips it over.
	w = openCappedLog(logPath, testCap)
	w.Write([]byte("y"))
	w.Write([]byte("z"))
	w.Close()

	if _, err := os.Stat(logPath + ".old"); err != nil {
		t.Fatalf("expected rotation after crossing cap: %v", err)
	}
}

func TestCappedLog_concurrentWrites(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")

	w := openCappedLog(logPath, testCap)
	defer w.Close()

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				w.Write([]byte("log line\n"))
			}
		}()
	}
	wg.Wait()
}

func TestCappedLog_closeSafeWhenOpenFails(t *testing.T) {
	w := openCappedLog("/nonexistent/dir/daemon.log", testCap)
	// Should not panic, should not close os.Stderr.
	if err := w.Close(); err != nil {
		t.Errorf("unexpected Close error on fallback writer: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
