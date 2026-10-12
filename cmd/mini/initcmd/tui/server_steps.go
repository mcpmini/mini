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

// serverSteps says what a server still needs. Only a login or a custom OAuth app is named; for
// anything else, env vars included, the step is the server's file, to keep the screens simple.
type serverSteps struct {
	file  func(name string) string
	login func(name string) string
}

// Steps name the file a server will have once Save and finish commits it, not its staging copy.
func newServerSteps(configDir string) serverSteps {
	mini := initcmd.MiniCommand(configDir)
	return serverSteps{
		file:  func(name string) string { return homeRelative(config.ServerPath(configDir, name)) },
		login: func(name string) string { return initcmd.LoginCommand(mini, name) },
	}
}

func (st serverSteps) byHand(status initcmd.ServerStatus) string {
	if status.Readiness == initcmd.NeedsOwnApp {
		return "requires a custom OAuth app, edit " + st.file(status.Name)
	}
	return "requires manual configuration, edit " + st.file(status.Name)
}

func (st serverSteps) state(status initcmd.ServerStatus, checking bool) string {
	switch {
	case checking:
		return "checking…"
	case status.Readiness == initcmd.Ready:
		return "✓ ready"
	case status.Readiness == initcmd.NeedsLogin:
		return "requires a login, run: " + st.login(status.Name)
	}
	return st.byHand(status)
}

// A file path is the step itself, so a long one wraps under its column instead of being cut at the window's edge.
func columnLines(name string, nameWidth int, text string, width int) []string {
	indent := strings.Repeat(" ", 2+nameWidth+2)
	parts := strings.Split(ansi.Wrap(text, max(width-len(indent), 20), "/"), "\n")
	lines := []string{fmt.Sprintf("  %-*s  %s", nameWidth, name, dim.Render(parts[0]))}
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
