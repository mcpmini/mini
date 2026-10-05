package initcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

type binaryLookup struct {
	executable func() (string, error)
	lookPath   func(file string) (string, error)
}

var systemLookup = binaryLookup{executable: os.Executable, lookPath: exec.LookPath}

// MiniCommand is what agents run to start mini with configDir. The --config flag is written
// only when configDir isn't the default, so the common entry stays short.
func MiniCommand(configDir string) agents.MiniEntry {
	return systemLookup.miniCommand(configDir)
}

func (l binaryLookup) miniCommand(configDir string) agents.MiniEntry {
	args := []string{"connect"}
	dir := configDir
	if abs, err := filepath.Abs(configDir); err == nil {
		dir = abs
	}
	if filepath.Clean(dir) != filepath.Clean(config.DefaultConfigDir()) {
		args = []string{"--config", dir, "connect"}
	}
	return agents.MiniEntry{Command: l.binaryPath(), Args: args}
}

// The PATH entry survives upgrades that replace the binary behind it (Homebrew, go install), so
// it is preferred whenever it is this same binary.
func (l binaryLookup) binaryPath() string {
	self, selfErr := l.executable()
	onPath, err := l.lookPath("mini")
	if err == nil {
		onPath, err = filepath.Abs(onPath)
	}
	switch {
	case err == nil && (selfErr != nil || sameFile(onPath, self)):
		return onPath
	case selfErr == nil:
		return self
	}
	return "mini"
}

type existingMini int

const (
	noMini existingMini = iota
	miniServesConfigDir
	miniElsewhere
)

func (p ApplyParams) existingMini(entries map[string]agents.Server) existingMini {
	found := noMini
	for name, entry := range entries {
		isMini := agents.IsMiniEntry(entry.Config, p.SelfPath)
		switch {
		case isMini && !entry.Disabled && sameDir(configDirArg(entry.Config.Args), p.ConfigDir):
			return miniServesConfigDir
		case isMini || name == agents.MiniKey:
			found = miniElsewhere
		}
	}
	return found
}

func configDirArg(args []string) string {
	for i, arg := range args {
		if dir, ok := strings.CutPrefix(arg, "--config="); ok {
			return dir
		}
		if arg == "--config" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return config.DefaultConfigDir()
}

func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

func sameFile(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}
