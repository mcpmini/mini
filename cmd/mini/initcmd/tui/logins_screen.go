package tui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/clock"
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
	startLogin func(ctx context.Context, configDir, name string) (Login, error)
	// configDirFor runs on the UI goroutine, which the run belongs to; the login runs on its own.
	configDirFor func(name string) string
	steps        serverSteps
	copy         func(text string) error
	// describe is what the catalog says a server URL does, or nothing for one it doesn't list.
	describe func(url string) string
	// clock paces the spinner; nil is the system clock.
	clock clock.Clock
}

type loginsScreen struct {
	p        loginsParams
	rows     []initcmd.ServerStatus
	byHand   []initcmd.ServerStatus
	checking map[string]bool
	err      error
	cursor   int
	results  map[string]error
	pending  *pendingLogin
	// notice says how the last copy went, until the next key.
	notice string
	logins int
	width  int
	scroll scroll
	// Until the user moves it, the cursor rests on the next login to do.
	cursorMoved bool
	// One waiting command covers every running check.
	waitingOnChecks bool
	spinnerFrame    int
}

func newLoginsScreen(p loginsParams) *loginsScreen {
	if p.clock == nil {
		p.clock = clock.System()
	}
	if p.describe == nil {
		p.describe = func(string) string { return "" }
	}
	return &loginsScreen{p: p, results: map[string]error{}}
}

func (s *loginsScreen) refresh() {
	selected := s.cursorName()
	// Checks before statuses: one that finishes in between still reads as running, and the change
	// it signals refreshes the screen again.
	s.checking = s.p.checking()
	statuses, err := s.p.statuses()
	s.rows, s.byHand, s.err = nil, nil, err
	for _, status := range statuses {
		switch {
		case s.isLoginRow(status):
			s.rows = append(s.rows, status)
		case !status.Ready():
			s.byHand = append(s.byHand, status)
		}
	}
	s.follow(selected)
}

// A tried login's row stays, so the user sees how it went.
func (s *loginsScreen) isLoginRow(status initcmd.ServerStatus) bool {
	_, tried := s.results[status.Name]
	return tried || s.checking[status.Name] || status.NeedsLogin()
}

// Rows come and go as checks finish, so the user's pick is found again by name.
func (s *loginsScreen) follow(name string) {
	i := slices.IndexFunc(s.rows, func(r initcmd.ServerStatus) bool { return r.Name == name })
	if s.cursorMoved && s.selectable(i) {
		s.cursor = i
		return
	}
	s.restOnNextLogin()
}

func (s *loginsScreen) cursorName() string {
	if s.cursor >= 0 && s.cursor < len(s.rows) {
		return s.rows[s.cursor].Name
	}
	return ""
}

func (s *loginsScreen) restOnNextLogin() {
	s.cursor = s.nextToLogIn()
	if s.cursor < 0 {
		// Resting on the last login lets up from Continue retry one.
		s.cursor = s.lastToLogIn()
	}
}

func (s *loginsScreen) focusable() bool {
	return s.selectable(s.cursor)
}

func (s *loginsScreen) finished() bool {
	return s.nextToLogIn() < 0
}

func (s *loginsScreen) takesEsc() bool {
	return s.pending != nil
}

func (s *loginsScreen) leave() {
	s.cancelLogin()
}

func (s *loginsScreen) forget(servers []string) {
	for _, name := range servers {
		delete(s.results, name)
	}
}

func (s *loginsScreen) enter() tea.Cmd {
	s.cursorMoved = false
	s.restOnNextLogin()
	return s.waitWhileChecking()
}

func (s *loginsScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case linkCopied:
		return s.linkCopied(msg)
	case checksChanged:
		s.waitingOnChecks = false
		s.refresh()
		return s.waitWhileChecking()
	case spinnerTicked:
		s.waitingOnChecks = false
		s.spinnerFrame++
		return s.waitWhileChecking()
	case loginStarted:
		return s.loginStarted(msg)
	case loginFinished:
		s.loginFinished(msg)
	}
	return nil
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

type spinnerTicked struct{}

// While the loading line shows, the wait also wakes for the spinner's next frame, so one wait is
// ever in flight.
func (s *loginsScreen) waitWhileChecking() tea.Cmd {
	if s.waitingOnChecks || len(s.checking) == 0 {
		return nil
	}
	s.waitingOnChecks = true
	var frame <-chan time.Time
	if s.loading() {
		frame = s.p.clock.After(spinnerInterval)
	}
	return func() tea.Msg {
		select {
		case <-s.p.changed:
			return checksChanged{}
		case <-frame:
			return spinnerTicked{}
		}
	}
}

// The checks usually finish within a second of arriving, so the rows appear once, settled, instead of
// flashing a state per row; once a login was tried, a later check never hides its result.
func (s *loginsScreen) loading() bool {
	return len(s.checking) > 0 && len(s.results) == 0 && s.pending == nil
}

func (s *loginsScreen) selectable(i int) bool {
	if s.loading() || i < 0 || i >= len(s.rows) {
		return false
	}
	r := s.rows[i]
	return r.NeedsLogin() && !s.checking[r.Name]
}

func (s *loginsScreen) nextToLogIn() int {
	for i, r := range s.rows {
		if _, tried := s.results[r.Name]; s.selectable(i) && !tried {
			return i
		}
	}
	return -1
}

func (s *loginsScreen) lastToLogIn() int {
	for i := len(s.rows) - 1; i >= 0; i-- {
		if s.selectable(i) {
			return i
		}
	}
	return -1
}

// move steps over the rows the cursor can't log in to; past the last server it reaches Continue.
func (s *loginsScreen) move(direction int) reply {
	s.cursorMoved = true
	for i := s.cursor + direction; i >= 0 && i < len(s.rows); i += direction {
		if s.selectable(i) {
			s.cursor = i
			return handled
		}
	}
	if direction > 0 {
		return pastLastRow
	}
	return handled
}

func (s *loginsScreen) handle(key tea.KeyPressMsg) (reply, tea.Cmd) {
	s.notice = ""
	switch key.String() {
	case "up", "down":
		return s.move(direction(key.String())), nil
	case "enter":
		return handled, s.startLogin(s.rows[s.cursor].Name)
	case "c":
		return handled, s.copyLink()
	case "esc":
		// On the rows, esc cancels a waiting login before it goes back, so a slip doesn't leave the screen.
		if s.takesEsc() {
			s.cancelLogin()
			return handled, nil
		}
	}
	return unhandled, nil
}

func (s *loginsScreen) resize(width, _ int) {
	s.width = width
}

func (s *loginsScreen) empty() bool {
	return s.err == nil && len(s.rows) == 0 && len(s.byHand) == 0
}
