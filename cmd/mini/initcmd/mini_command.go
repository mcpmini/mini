package initcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// ExistingMini is what an agent already had for mini. That entry is the user's: apply never
// changes it or adds a second one.
type ExistingMini int

const (
	NoMiniEntry ExistingMini = iota
	// MiniEntryServes: switched on and running the config directory init set up.
	MiniEntryServes
	// MiniEntryInactive: switched off, running another config directory, not known to start, or
	// another server under mini's key; the agent may not get this mini's servers through it.
	MiniEntryInactive
)

func (p ApplyParams) existingMini(entries map[string]agents.Server) ExistingMini {
	found := NoMiniEntry
	for name, entry := range entries {
		isMini := agents.IsMiniEntry(entry.Config, p.SelfPath)
		switch {
		case isMini && p.serves(entry.Config, entry.Disabled):
			return MiniEntryServes
		case isMini || name == agents.MiniKey:
			found = MiniEntryInactive
		}
	}
	return found
}

// Only an absolute path to a binary that's still there is known to start: an agent may run with
// another PATH than init (GUI apps get the system's minimal one), an upgrade can move the binary,
// and mini has no serve command any more.
func (p ApplyParams) serves(sc config.ServerConfig, disabled bool) bool {
	_, err := exec.LookPath(sc.Command)
	return !disabled && filepath.IsAbs(sc.Command) && err == nil && slices.Contains(sc.Args, "connect") &&
		sameDir(configDirArg(sc.Args), p.ConfigDir)
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
	return (errA == nil && errB == nil && absA == absB) || sameFile(a, b)
}

func sameFile(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}
