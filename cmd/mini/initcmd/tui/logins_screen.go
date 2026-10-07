package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

// Login is a browser login that has started: the browser is opening URL, and Wait blocks until the
// login ends and its token is saved.
type Login struct {
	URL  string
	Wait func() error
}

type checksChanged struct{}

type loginsParams struct {
	statuses   func() ([]initcmd.ServerStatus, error)
	checking   func() map[string]bool
	changed    <-chan struct{}
	startLogin func(ctx context.Context, name string) (Login, error)
}

// loginsScreen lists the configured servers that don't work yet and logs in to the OAuth ones.
// The cursor moves over the rows a login can fix, then the Continue row.
type loginsScreen struct {
	p        loginsParams
	rows     []initcmd.ServerStatus
	checking map[string]bool
	err      error
	cursor   int
	results  map[string]error
	pending  *pendingLogin
	logins   int
	// One waiting command covers every running check.
	waiting bool
}

func newLoginsScreen(p loginsParams) *loginsScreen {
	return &loginsScreen{p: p, results: map[string]error{}}
}

// A row stays once a login on it finished, so the user sees how it went. The checks are read
// before the statuses: a check that finishes in between still reads as running, and the change
// it signals refreshes the screen again.
func (s *loginsScreen) refresh() {
	// Checks before statuses: one that finishes in between still reads as running, and the change
	// it signals refreshes the screen again.
	s.checking = s.p.checking()
	statuses, err := s.p.statuses()
	s.rows, s.err = nil, err
	for _, status := range statuses {
		_, tried := s.results[status.Name]
		if status.Readiness != initcmd.Ready || s.checking[status.Name] || tried {
			s.rows = append(s.rows, status)
		}
	}
	if !s.selectable(s.cursor) {
		s.cursor = s.nextToLogIn(-1)
	}
}

func (s *loginsScreen) enter() tea.Cmd {
	s.refresh()
	s.cursor = s.nextToLogIn(-1)
	return s.waitWhileChecking()
}

func (s *loginsScreen) start() tea.Cmd {
	return nil
}

func (s *loginsScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case checksChanged:
		s.waiting = false
		onContinue := s.cursor == len(s.rows)
		s.refresh()
		if onContinue {
			s.cursor = s.nextToLogIn(-1)
		}
		return s.waitWhileChecking()
	case loginStarted:
		return s.loginStarted(msg)
	case loginFinished:
		s.loginFinished(msg)
	}
	return nil
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

// The Continue row sits at len(rows); token, app and env rows are greyed, since a login can't fix them.
func (s *loginsScreen) selectable(i int) bool {
	if i == len(s.rows) {
		return true
	}
	if i < 0 || i > len(s.rows) {
		return false
	}
	r := s.rows[i]
	return r.Readiness == initcmd.NeedsLogin && !s.checking[r.Name]
}

// nextToLogIn is the first row after from that needs a login not tried yet, else Continue.
func (s *loginsScreen) nextToLogIn(from int) int {
	for i := from + 1; i < len(s.rows); i++ {
		if _, tried := s.results[s.rows[i].Name]; s.selectable(i) && !tried {
			return i
		}
	}
	return len(s.rows)
}

func (s *loginsScreen) move(direction int) {
	for i := s.cursor + direction; i >= 0 && i <= len(s.rows); i += direction {
		if s.selectable(i) {
			s.cursor = i
			return
		}
	}
}

func (s *loginsScreen) handle(key tea.KeyPressMsg) (step, tea.Cmd) {
	switch key.String() {
	case "up":
		s.move(-1)
	case "down":
		s.move(1)
	case "enter":
		if s.cursor == len(s.rows) {
			s.cancelLogin()
			return forward, nil
		}
		return stay, s.startLogin(s.rows[s.cursor].Name)
	case "esc", "left", "shift+tab":
		s.cancelLogin()
		return back, nil
	}
	return stay, nil
}

func (s *loginsScreen) heading() string {
	if s.err == nil && len(s.rows) == 0 {
		return "Your servers are ready"
	}
	return "Finish setting up these servers"
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
	for i, status := range s.rows {
		lines = append(lines, s.cursorMark(i)+fmt.Sprintf("%-*s  %s", width, status.Name, s.state(status)))
	}
	if len(lines) == 0 {
		// The last check cleared the final row while the screen was shown.
		lines = append(lines, "Every server works; nothing is left to set up.", "")
	}
	return strings.Join(append(lines, s.cursorMark(len(s.rows))+"Continue →"), "\n")
}

func (s *loginsScreen) cursorMark(i int) string {
	if i == s.cursor {
		return "> "
	}
	return "  "
}

func (s *loginsScreen) state(status initcmd.ServerStatus) string {
	if s.pending != nil && s.pending.name == status.Name {
		if s.pending.url == "" {
			return "starting the login…"
		}
		return "waiting for the browser… " + dim.Render(s.pending.url)
	}
	if err, tried := s.results[status.Name]; tried {
		if err != nil {
			return "✗ " + err.Error()
		}
		return "✓ logged in"
	}
	return dim.Render(s.need(status))
}

func (s *loginsScreen) need(status initcmd.ServerStatus) string {
	if s.checking[status.Name] {
		return "checking…"
	}
	switch status.Readiness {
	case initcmd.NeedsLogin:
		return "needs a login"
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
	if s.cursor < len(s.rows) {
		return "↑↓ move · enter log in"
	}
	return "↑↓ move · enter continue"
}

func (s *loginsScreen) empty() bool {
	s.refresh()
	return s.err == nil && len(s.rows) == 0
}
