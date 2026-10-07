package initcmd

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/fileio"
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
	defaultDir, err := config.DefaultConfigDir()
	if err != nil || filepath.Clean(dir) != filepath.Clean(defaultDir) {
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

func sameFile(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

// TemporaryMini is the binary agents would run when it sits where files don't last: the temp
// directory, where go run builds, Go's build cache, or Downloads; else "". Agents fail once it
// is cleaned up.
func (s Setup) TemporaryMini() string {
	path := MiniCommand(s.ConfigDir).Command
	for _, dir := range temporaryDirs() {
		if fileio.Within(path, dir) {
			return path
		}
	}
	return ""
}

func temporaryDirs() []string {
	dirs := []string{os.TempDir()}
	if gotmp := os.Getenv("GOTMPDIR"); gotmp != "" {
		dirs = append(dirs, gotmp)
	}
	if cache := os.Getenv("GOCACHE"); cache != "" {
		dirs = append(dirs, cache)
	} else if cache, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(cache, "go-build"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Downloads"))
	}
	return dirs
}
