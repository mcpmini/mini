package agents

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/mcpmini/mini/internal/fileio"
)

// MiniEntry is the command an agent runs to start mini.
type MiniEntry struct {
	Command string
	Args    []string
}

// The key mini is written under; an agent's existing entry there is the user's and never changed.
const MiniKey = "mini"

func connectJSON(config []byte, remove []string, mini MiniEntry) ([]byte, error) {
	if len(bytes.TrimSpace(config)) == 0 {
		config = []byte("{}")
	}
	entry := map[string]any{"command": mini.Command, "args": mini.Args}
	return EditJSONServers(config, remove, map[string]any{MiniKey: entry})
}

func connectCodex(config []byte, disable []string, mini MiniEntry) ([]byte, error) {
	return EditCodexServers(config, disable, CodexServer(mini))
}

// CreateFile writes the MCP config of an agent that has none yet. It never replaces a file: one
// that appeared meanwhile is the agent's, and must go through EditFile.
func CreateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return fileio.CreateFile(path, data, 0600)
}
