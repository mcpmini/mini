package tui

import (
	"context"
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
	startLogin func(ctx context.Context, name string) (Login, error)
}

// loginsScreen lists the configured servers that don't work yet and logs in to the OAuth ones.
type loginsScreen struct {
	p        loginsParams
	rows     []initcmd.ServerStatus
	checking map[string]bool
	err      error
	cursor   int
	results  map[string]error
	pending  *pendingLogin
	logins   int
	width    int
	// moved is true once the user moved the cursor since the last login ended; until then the
	// cursor rests on the next login to do.
	moved bool
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
	selected := s.cursorName()
	s.checking = s.p.checking()
	statuses, err := s.p.statuses()
	s.rows, s.err = nil, err
	for _, status := range statuses {
		_, tried := s.results[status.Name]
		if status.Readiness != initcmd.Ready || s.checking[status.Name] || tried {
			s.rows = append(s.rows, status)
		}
	}
	sections := []string{logInSection, byHandSection}
	slices.SortStableFunc(s.rows, func(a, b initcmd.ServerStatus) int {
		return slices.Index(sections, s.section(a)) - slices.Index(sections, s.section(b))
	})
	// Rows come and go as checks finish, so the user's pick is found again by name.
	s.cursor = s.nextToLogIn(-1)
	if i := s.rowIndex(selected); s.moved && s.selectable(i) {
		s.cursor = i
	}
}

// cursorName is the server under the cursor, or "" on Continue.
func (s *loginsScreen) cursorName() string {
	if s.cursor >= 0 && s.cursor < len(s.rows) {
		return s.rows[s.cursor].Name
	}
	return ""
}

func (s *loginsScreen) rowIndex(name string) int {
	if name == "" {
		return len(s.rows)
	}
	return slices.IndexFunc(s.rows, func(r initcmd.ServerStatus) bool { return r.Name == name })
}

func (s *loginsScreen) enter() tea.Cmd {
	s.moved = false
	s.refresh()
	return s.waitWhileChecking()
}

func (s *loginsScreen) start() tea.Cmd {
	return nil
}

func (s *loginsScreen) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case checksChanged:
		s.waiting = false
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
	if s.waiting || len(s.checking) == 0 {
		return nil
	}
	s.waiting = true
	return func() tea.Msg {
		<-s.p.changed
		return checksChanged{}
	}
}

// The Continue row sits at len(rows); only a row needing a login, and no longer checking, takes the cursor.
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
			s.cursor, s.moved = i, true
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
	section := ""
	for i, status := range s.rows {
		if s.section(status) != section {
			if section != "" {
				lines = append(lines, "")
			}
			section = s.section(status)
			lines = append(lines, bold.Render(section))
		}
		lines = append(lines, s.cursorMark(i)+fmt.Sprintf("%-*s  %s", width, status.Name, s.state(status)))
		if s.pending != nil && s.pending.name == status.Name && s.pending.url != "" {
			lines = append(lines, s.urlLines(4+width)...)
		}
	}
	if len(lines) == 0 {
		// The last check cleared the final row while the screen was shown.
		lines = append(lines, "Every server works; nothing is left to set up.")
	}
	return strings.Join(append(lines, "", s.cursorMark(len(s.rows))+"Continue →"), "\n")
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

// Authorize URLs run to hundreds of characters and the app cuts lines at the window's edge, so the
// URL is wrapped onto lines of its own; each links to the whole URL where terminals support links.
func (s *loginsScreen) urlLines(indent int) []string {
	var lines []string
	for _, part := range strings.Split(ansi.Hardwrap(s.pending.url, max(s.width-indent, 20), false), "\n") {
		lines = append(lines, strings.Repeat(" ", indent)+ansi.SetHyperlink(s.pending.url)+part+ansi.ResetHyperlink())
	}
	return lines
}

func (s *loginsScreen) resize(width int) {
	s.width = width
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
		return "waiting for the browser; if it didn't open, use this link:"
	}
	if err, tried := s.results[status.Name]; tried {
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

func (s *loginsScreen) keys() string {
	if s.pending != nil {
		return "↑↓ move"
	}
	if s.cursor < len(s.rows) {
		return "↑↓ move · enter log in"
	}
	return "↑↓ move · enter continue"
}

func (s *loginsScreen) empty() bool {
	s.refresh()
	return s.err == nil && len(s.rows) == 0
}
