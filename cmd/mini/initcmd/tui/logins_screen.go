package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

type checksChanged struct{}

type loginsParams struct {
	configDir string
	statuses  func() ([]initcmd.ServerStatus, error)
	checking  func() map[string]bool
	changed   <-chan struct{}
}

type loginsScreen struct {
	p        loginsParams
	rows     []initcmd.ServerStatus
	checking map[string]bool
	err      error
	// One waiting command covers every running check.
	waiting bool
}

func newLoginsScreen(p loginsParams) *loginsScreen {
	return &loginsScreen{p: p}
}

func (s *loginsScreen) refresh() {
	// Checks before statuses: one that finishes in between still reads as running, and the change
	// it signals refreshes the screen again.
	s.checking = s.p.checking()
	statuses, err := s.p.statuses()
	s.rows, s.err = nil, err
	for _, status := range statuses {
		if status.Readiness != initcmd.Ready || s.checking[status.Name] {
			s.rows = append(s.rows, status)
		}
	}
}

func (s *loginsScreen) enter() tea.Cmd {
	s.refresh()
	return s.waitWhileChecking()
}

func (s *loginsScreen) start() tea.Cmd {
	return nil
}

func (s *loginsScreen) update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(checksChanged); !ok {
		return nil
	}
	s.waiting = false
	s.refresh()
	return s.waitWhileChecking()
}

func (s *loginsScreen) waitWhileChecking() tea.Cmd {
	if s.waiting || len(s.checking) == 0 {
		return nil
	}
	s.waiting = true
	return func() tea.Msg {
		<-s.p.changed
		return checksChanged{}
	}
}

func (s *loginsScreen) heading() string {
	if s.err == nil && len(s.rows) == 0 {
		return "Your servers are ready"
	}
	return "Finish setting up these servers"
}

func (s *loginsScreen) handle(key tea.KeyPressMsg) step {
	switch key.String() {
	case "enter":
		return forward
	case "esc", "left", "shift+tab":
		return back
	}
	return stay
}

func (s *loginsScreen) body(int) string {
	if s.err != nil {
		return "mini's servers couldn't be read: " + s.err.Error()
	}
	width := 0
	for _, status := range s.rows {
		width = max(width, len(status.Name))
	}
	var lines []string
	for _, status := range s.rows {
		lines = append(lines, fmt.Sprintf("  %-*s  %s", width, status.Name, dim.Render(s.need(status))))
	}
	if len(lines) == 0 {
		// The last check cleared the final row while the screen was shown.
		return "Every server works; nothing is left to set up."
	}
	return strings.Join(lines, "\n")
}

func (s *loginsScreen) need(status initcmd.ServerStatus) string {
	if s.checking[status.Name] {
		return "checking…"
	}
	switch status.Readiness {
	case initcmd.NeedsLogin:
		return "needs a login; " + initcmd.SetupStep(s.p.configDir, status)
	case initcmd.NeedsToken:
		return needsToken
	case initcmd.NeedsOwnApp:
		return needsOwnApp
	case initcmd.NeedsEnv:
		return "needs " + strings.Join(status.UnsetEnv.Names, ", ") + " set"
	}
	return ""
}

func (s *loginsScreen) keys() string {
	return "enter continue"
}

func (s *loginsScreen) empty() bool {
	s.refresh()
	return s.err == nil && len(s.rows) == 0
}
