package agents

import (
	"maps"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/mcpmini/mini/internal/config"
)

// ConnectionDifferences names the connection fields where two configs differ. Agent entries keep
// their tokens in headers and env, so the same URL with a different token is a different server.
func ConnectionDifferences(a, b config.ServerConfig) []string {
	fields := []struct {
		name string
		same bool
	}{
		{"transport", transportOrStdio(a.Transport) == transportOrStdio(b.Transport)},
		{"url", a.URL == b.URL},
		{"command", a.Command == b.Command},
		{"args", slices.Equal(a.Args, b.Args)},
		{"env", slices.Equal(slices.Sorted(slices.Values(a.Env)), slices.Sorted(slices.Values(b.Env)))},
		{"headers", maps.Equal(a.Headers, b.Headers)},
	}
	var differences []string
	for _, field := range fields {
		if !field.same {
			differences = append(differences, field.name)
		}
	}
	return differences
}

func transportOrStdio(transport string) string {
	if transport == "" {
		return "stdio"
	}
	return transport
}

// IsMiniEntry reports whether an agent entry runs mini itself, so mini is never imported into
// mini. A command named mini with connect or serve counts even when it isn't this binary.
func IsMiniEntry(sc config.ServerConfig, selfPath string) bool {
	if isSelfCommand(sc.Command, selfPath) {
		return true
	}
	base := filepath.Base(sc.Command)
	return (base == "mini" || base == "mini.exe") &&
		(slices.Contains(sc.Args, "connect") || slices.Contains(sc.Args, "serve"))
}

// isSelfCommand handles bare names, symlinks, and alternate build paths of the running binary.
func isSelfCommand(cmd, selfPath string) bool {
	if cmd == "" || selfPath == "" {
		return false
	}
	resolved := resolveExe(cmd)
	return resolved == resolveExe(selfPath) || resolved == resolveExe(filepath.Base(selfPath))
}

// resolveExe returns p unchanged when it can't be resolved.
func resolveExe(p string) string {
	if !filepath.IsAbs(p) {
		if found, err := exec.LookPath(p); err == nil {
			p = found
		}
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
