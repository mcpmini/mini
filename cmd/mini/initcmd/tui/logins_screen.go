package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	startLogin func(ctx context.Context, name string) (Login, error)
	copy       func(text string) error
}

type loginsScreen struct {
	p        loginsParams
	rows     []initcmd.ServerStatus
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
	s.rows, s.err = nil, err
	for _, status := range statuses {
		// A tried login's row stays, so the user sees how it went.
		_, tried := s.results[status.Name]
		if status.Readiness != initcmd.Ready || s.checking[status.Name] || tried {
			s.rows = append(s.rows, status)
		}
	}
	sections := []string{logInSection, byHandSection}
	slices.SortStableFunc(s.rows, func(a, b initcmd.ServerStatus) int {
		return slices.Index(sections, s.section(a)) - slices.Index(sections, s.section(b))
	})
	s.follow(selected)
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
		if s.pending != nil {
			s.cancelLogin()
			return handled, nil
		}
	}
	return unhandled, nil
}

func (s *loginsScreen) heading() string {
	if s.err == nil && len(s.rows) == 0 {
		return "Your servers are ready"
	}
	return "Finish setting up these servers"
}

func (s *loginsScreen) body(height int, focused bool) string {
	if s.err != nil {
		return "mini's servers couldn't be read: " + s.err.Error()
	}
	lines, first, last := s.serverLines(focused)
	return strings.Join(s.scroll.cut(lines, first, last, height), "\n")
}

// first and last bound the cursor's block: its section heading, its row and the lines under it.
func (s *loginsScreen) serverLines(focused bool) (lines []string, first, last int) {
	width := widest(s.rows, func(status initcmd.ServerStatus) string { return status.Name })
	section := ""
	for i, status := range s.rows {
		start := len(lines)
		lines, section = s.withSectionHeading(lines, section, status)
		// The cursor's server stays in view while the cursor is on Continue, so a login's result shows.
		if i == s.cursor {
			first = start
		}
		lines = append(lines, s.rowLines(status, focused && i == s.cursor, width)...)
		if i == s.cursor {
			last = len(lines) - 1
		}
	}
	if len(lines) == 0 {
		// The last check cleared the final row while the screen was shown.
		lines = append(lines, "Every server works; nothing is left to set up.")
	}
	return lines, first, last
}

func (s *loginsScreen) rowLines(status initcmd.ServerStatus, atCursor bool, width int) []string {
	lines := []string{cursorMark(atCursor) + fmt.Sprintf("%-*s  %s", width, status.Name, s.state(status))}
	if s.pending != nil && s.pending.name == status.Name && s.pending.url != "" {
		lines = append(lines, s.linkLines(4+width)...)
	}
	return lines
}

func (s *loginsScreen) withSectionHeading(
	lines []string,
	current string,
	status initcmd.ServerStatus,
) ([]string, string) {
	next := s.section(status)
	if next == current {
		return lines, current
	}
	if current != "" {
		lines = append(lines, "")
	}
	return append(lines, bold.Render(next)), next
}

const (
	logInSection  = "Log in"
	byHandSection = "Set up by hand"
)

// The cursor only stops on servers it can log in to, so the rest are grouped apart from them.
func (s *loginsScreen) section(status initcmd.ServerStatus) string {
	if _, tried := s.results[status.Name]; tried || status.Readiness == initcmd.NeedsLogin || s.checking[status.Name] {
		return logInSection
	}
	return byHandSection
}

// Authorize URLs run to hundreds of characters, so the link shows its host and as much of the path as
// fits: the host is what tells the user which server they are signing in to. c copies the whole URL.
// The app cuts lines at the window's edge, so the whole link wraps to stay selectable.
func (s *loginsScreen) linkLines(indent int) []string {
	if !s.pending.revealed {
		return []string{s.linkLine(indent)}
	}
	var lines []string
	for _, line := range strings.Split(ansi.Hardwrap(s.pending.url, max(s.width-indent, 20), false), "\n") {
		lines = append(lines, strings.Repeat(" ", indent)+line)
	}
	return lines
}

func (s *loginsScreen) linkLine(indent int) string {
	u, err := url.Parse(s.pending.url)
	text := s.pending.url
	if err == nil && u.Host != "" {
		text = u.Host + u.EscapedPath()
		if u.RawQuery != "" {
			text += "?" + u.RawQuery
		}
	}
	host := len(text)
	if err == nil {
		host = len(u.Host)
	}
	shown := ansi.Truncate(text, max(s.width-indent, host+1), "…")
	return strings.Repeat(" ", indent) + ansi.SetHyperlink(s.pending.url) + shown + ansi.ResetHyperlink()
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
			return "✗ timed out; enter to try again"
		}
		if err != nil {
			return "✗ " + firstLine(err)
		}
		return "✓ logged in"
	}
	return dim.Render(s.need(status))
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

func withNotice(notice, keys string) string {
	if notice == "" {
		return keys
	}
	return notice + " · " + keys
}

func (s *loginsScreen) keys() string {
	if s.pending != nil {
		return withNotice(s.notice, "↑↓ move · c copy link · esc cancel login · tab continue")
	}
	return "↑↓ move · enter log in · tab continue"
}

func (s *loginsScreen) empty() bool {
	return s.err == nil && len(s.rows) == 0
}
