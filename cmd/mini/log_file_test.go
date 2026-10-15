package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestLogFile(t *testing.T) {
	t.Run("a run that logs nothing leaves no file", func(t *testing.T) {
		configDir := t.TempDir()
		logs := newLogFile(configDir, "test.log")
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
		logs := newLogFile(t.TempDir(), "test.log")
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
			t.Error("the log still writes to the file after restore")
		}
	})
	t.Run("a later run keeps what earlier runs logged", func(t *testing.T) {
		configDir := t.TempDir()
		for _, line := range []string{"first run", "second run"} {
			logs := newLogFile(configDir, "test.log")
			restore := logs.redirect()
			log.Print(line)
			restore()
		}
		data := string(testutil.ReadFile(t, filepath.Join(configDir, "internal", "test.log")))
		if !strings.Contains(data, "first run") || !strings.Contains(data, "second run") {
			t.Errorf("log file = %q, want both runs", data)
		}
	})
}
