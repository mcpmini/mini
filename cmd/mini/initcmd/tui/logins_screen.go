package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

type checksChanged struct{}

type loginsParams struct {
	statuses func() ([]initcmd.ServerStatus, error)
	// checking names the servers whose OAuth check is running.
	checking func() map[string]bool
	changed  <-chan struct{}
}

// loginsScreen lists the configured servers that don't work yet and what each one needs.
type loginsScreen struct {
	p        loginsParams
	rows     []initcmd.ServerStatus
	checking map[string]bool
	err      error
	// waiting is true while a command waits for the next check to finish; one is enough.
	waiting bool
}

func newLoginsScreen(p loginsParams) *loginsScreen {
	return &loginsScreen{p: p}
}

// The checks are read before the statuses: a check that finishes in between still reads as
// running, and the change it signals refreshes the screen again.
func (s *loginsScreen) refresh() {
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

// Each check that finishes can change a row, so the screen waits for the next one while any run.
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
	return strings.Join(lines, "\n")
}

func (s *loginsScreen) need(status initcmd.ServerStatus) string {
	if s.checking[status.Name] {
		return "checking…"
	}
	switch status.Readiness {
	case initcmd.NeedsLogin:
		return "needs a login: mini auth " + status.Name
	case initcmd.NeedsToken:
		return "needs a token"
	case initcmd.NeedsOwnApp:
		return "needs your own OAuth app"
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
