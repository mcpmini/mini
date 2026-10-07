package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestUILogs(t *testing.T) {
	t.Run("a run that logs nothing leaves no file", func(t *testing.T) {
		configDir := t.TempDir()
		logs := newUILogs(configDir)
		logs.redirect()()
		if path, ok := logs.written(); ok {
			t.Errorf("written = %s, want nothing", path)
		}
		if _, err := os.Stat(filepath.Join(configDir, "internal")); !os.IsNotExist(err) {
			t.Errorf("internal dir: %v, want it never created", err)
		}
	})
	t.Run("what is logged lands in the file, and the log goes back afterward", func(t *testing.T) {
		before := log.Writer()
		logs := newUILogs(t.TempDir())
		restore := logs.redirect()
		log.Print("probe failed")
		restore()
		path, ok := logs.written()
		if !ok {
			t.Fatal("written = false after a log line")
		}
		if data := testutil.ReadFile(t, path); !strings.Contains(string(data), "probe failed") {
			t.Errorf("log file = %q, want the logged line", data)
		}
		if log.Writer() != before {
			t.Error("the log still writes to the UI file after restore")
		}
	})
}
