//go:build unix

package importers

import (
	"strings"
	"syscall"
	"testing"
)

func TestReadConfigFileRejectsNonRegularFile(t *testing.T) {
	dir := tempDir(t)
	fifo := dir + "/test.fifo"
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	_, err := ReadConfigFile(fifo)
	if err == nil {
		t.Fatal("expected error for FIFO, got nil")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %q, want 'not a regular file'", err.Error())
	}
}
