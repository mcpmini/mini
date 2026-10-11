package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
	startLogin func(ctx context.Context, configDir, name string) (Login, error)
	// configDirFor runs on the UI goroutine, which the run belongs to; the login runs on its own.
	configDirFor func(name string) string
	copy         func(text string) error
}

type loginsScreen struct {
	p        loginsParams
	rows     []initcmd.ServerStatus
	byHand   []string
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
}

func newLoginsScreen(p loginsParams) *loginsScreen {
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
		case status.Readiness != initcmd.Ready:
			s.byHand = append(s.byHand, status.Name)
		}
	}
	s.follow(selected)
}

// A tried login's row stays, so the user sees how it went.
func (s *loginsScreen) isLoginRow(status initcmd.ServerStatus) bool {
	_, tried := s.results[status.Name]
	return tried || s.checking[status.Name] || status.Readiness == initcmd.NeedsLogin
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
	case loginStarted:
		return s.loginStarted(msg)
	case loginFinished:
		s.loginFinished(msg)
	}
	return nil
}

func (s *loginsScreen) waitWhileChecking() tea.Cmd {
	if s.waitingOnChecks || len(s.checking) == 0 {
		return nil
	}
	s.waitingOnChecks = true
	return func() tea.Msg {
		<-s.p.changed
		return checksChanged{}
	}
}

func (s *loginsScreen) selectable(i int) bool {
	if i < 0 || i >= len(s.rows) {
		return false
	}
	r := s.rows[i]
	return r.Readiness == initcmd.NeedsLogin && !s.checking[r.Name]
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

func (s *loginsScreen) heading() string {
	if len(s.rows) > 0 {
		return "Log in to your MCPs (optional)"
	}
	if s.empty() {
		return "Your servers are ready"
	}
	if s.err != nil {
		return "Log in to your MCPs (optional)"
	}
	return "Finish setting up these MCPs later"
}

func (s *loginsScreen) body(height int, focused bool) string {
	if s.err != nil {
		return "mini's servers couldn't be read: " + s.err.Error()
	}
	intro := s.byHandIntro()
	rows, first, last := s.serverLines(focused)
	if len(s.rows) > 0 {
		first, last = first+len(intro), last+len(intro)
	}
	return strings.Join(s.scroll.cut(append(intro, rows...), first, last, height), "\n")
}

func (s *loginsScreen) byHandIntro() []string {
	if len(s.byHand) == 0 {
		return nil
	}
	them := "these"
	if len(s.byHand) == 1 {
		them = "it"
	}
	text := fmt.Sprintf(
		"Browser login isn't available for %s. You will need to configure %s manually later.",
		initcmd.JoinAnd(s.byHand), them,
	)
	lines := strings.Split(ansi.Wordwrap(text, max(s.width, 20), ""), "\n")
	if len(s.rows) > 0 {
		lines = append(lines, "")
	}
	return lines
}

// first and last bound the cursor's block: its row and the lines under it.
func (s *loginsScreen) serverLines(focused bool) (lines []string, first, last int) {
	width := widest(s.rows, func(status initcmd.ServerStatus) string { return status.Name })
	for i, status := range s.rows {
		start := len(lines)
		// The cursor's server stays in view while the cursor is on Continue, so a login's result shows.
		if i == s.cursor {
			first = start
		}
		lines = append(lines, s.rowLines(status, focused && i == s.cursor, width)...)
		if i == s.cursor {
			last = len(lines) - 1
		}
	}
	if len(lines) == 0 && len(s.byHand) == 0 {
		// The last check cleared the final row while the screen was shown.
		lines = append(lines, "Every server works; nothing is left to set up.")
	}
	return lines, first, last
}

func (s *loginsScreen) rowLines(status initcmd.ServerStatus, atCursor bool, width int) []string {
	lines := []string{cursorMark(atCursor) + s.rowText(status, width)}
	if s.pending != nil && s.pending.name == status.Name && s.pending.url != "" {
		lines = append(lines, s.urlLines(4+width)...)
	}
	if hint := s.hint(status); atCursor && hint != "" {
		lines = append(lines, cursorMark(false)+dim.Render(hint))
	}
	return lines
}

func (s *loginsScreen) rowText(status initcmd.ServerStatus, width int) string {
	state := s.state(status)
	if state == "" {
		return status.Name
	}
	return fmt.Sprintf("%-*s  %s", width, status.Name, state)
}

func (s *loginsScreen) hint(status initcmd.ServerStatus) string {
	if s.checking[status.Name] || s.pending != nil {
		return ""
	}
	err, tried := s.results[status.Name]
	if !tried {
		return "press enter to log in"
	}
	if err != nil {
		return "press enter to try again"
	}
	return ""
}

// Authorize URLs run to hundreds of characters, so the link shows its host and as much of the path as
// fits: the host is what tells the user which server they are signing in to. c copies the whole URL.
// Authorize URLs run to hundreds of characters, and the app cuts lines at the window's edge.
func (s *loginsScreen) urlLines(indent int) []string {
	var lines []string
	for _, part := range strings.Split(ansi.Hardwrap(s.pending.url, max(s.width-indent, 20), false), "\n") {
		lines = append(lines, strings.Repeat(" ", indent)+ansi.SetHyperlink(s.pending.url)+part+ansi.ResetHyperlink())
	}
	return lines
}

func (s *loginsScreen) resize(width, _ int) {
	s.width = width
}

func (s *loginsScreen) state(status initcmd.ServerStatus) string {
	if s.pending != nil && s.pending.name == status.Name {
		if s.pending.url == "" {
			return "starting the login…"
		}
		return "waiting for the browser; if it didn't open, use this link:"
	}
	if err, tried := s.results[status.Name]; tried {
		if errors.Is(err, context.DeadlineExceeded) {
			return "✗ login timed out"
		}
		if err != nil {
			return "✗ login failed: " + firstLine(err)
		}
		return "✓ logged in"
	}
	if s.checking[status.Name] {
		return dim.Render("checking…")
	}
	return ""
}

// A failed token exchange's error carries the endpoint's raw response body, often a whole HTML
// page; its lines would push the rows below off the screen.
func firstLine(err error) string {
	line, _, _ := strings.Cut(ansi.Strip(err.Error()), "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, line)
}

func withNotice(notice, keys string) string {
	if notice == "" {
		return keys
	}
	return notice + " · " + keys
}

func (s *loginsScreen) keys() string {
	if s.pending != nil && s.pending.url == "" {
		return "↑↓ move · esc cancel login · tab continue"
	}
	if s.pending != nil {
		return withNotice(s.notice, "↑↓ move · c copy link · esc cancel login · tab continue")
	}
	return "↑↓ move · enter open browser · tab continue"
}

func (s *loginsScreen) empty() bool {
	return s.err == nil && len(s.rows) == 0 && len(s.byHand) == 0
}
