package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/config"
)

// serverSteps fills in the file and login command a server's step names.
type serverSteps struct {
	file  func(name string) string
	login func(name string) string
}

// Steps name the file a server will have once Save and finish commits it, not its staging copy.
func newServerSteps(configDir string) serverSteps {
	return serverSteps{
		file:  func(name string) string { return homeRelative(config.ServerPath(configDir, name)) },
		login: initcmd.LoginCommands(configDir),
	}
}

func (st serverSteps) step(status initcmd.ServerStatus) string {
	return status.Step(st.file(status.Name), st.login(status.Name))
}

func (st serverSteps) state(status initcmd.ServerStatus, checking bool) string {
	switch {
	case checking:
		return "checking…"
	case status.Ready():
		return "✓ ready"
	}
	return st.step(status)
}

// A file path is the step itself, so a long one wraps under its column instead of being cut at the window's edge.
func columnLines(params columnLinesParams) []string {
	indent := strings.Repeat(" ", 2+params.NameWidth+2)
	parts := strings.Split(ansi.Wrap(params.Text, max(params.Width-len(indent), 20), "/"), "\n")
	lines := []string{fmt.Sprintf("  %-*s  %s", params.NameWidth, params.Name, dim.Render(parts[0]))}
	for _, part := range parts[1:] {
		lines = append(lines, indent+dim.Render(part))
	}
	return lines
}

func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return path
}

type columnLinesParams struct {
	Name      string
	NameWidth int
	Text      string
	Width     int
}
