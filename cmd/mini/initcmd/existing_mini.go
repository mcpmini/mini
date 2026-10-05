package initcmd

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

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

// Only absolute paths are known to mean the same thing to the agent: it may run with another PATH
// and working directory than init (GUI apps get the system's minimal PATH). An upgrade can move
// the binary, and mini has no serve command any more.
func (p ApplyParams) serves(sc config.ServerConfig, disabled bool) bool {
	_, err := exec.LookPath(sc.Command)
	dir := configDirArg(sc.Args)
	return !disabled && filepath.IsAbs(sc.Command) && err == nil && slices.Contains(sc.Args, "connect") &&
		filepath.IsAbs(dir) && sameDir(dir, p.ConfigDir)
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
