//go:build unix

package importers

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadConfigFileReadsAPipe(t *testing.T) {
	fifo := filepath.Join(tempDir(t), "config.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	go func() {
		os.WriteFile(fifo, []byte(`{"mcpServers":{}}`), 0600) //nolint:errcheck // a failed write fails the read below
	}()

	data, err := ReadConfigFile(fifo)

	if err != nil || string(data) != `{"mcpServers":{}}` {
		t.Errorf("ReadConfigFile(pipe) = %q, %v; want the written config", data, err)
	}
}
