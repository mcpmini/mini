package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (s *loginsScreen) heading() string {
	switch {
	case len(s.rows) > 0 || s.err != nil:
		return "Log in to your MCPs (optional)"
	case len(s.byHand) > 0:
		return "Finish setting up these MCPs later"
	}
	return "Your MCPs are ready"
}

func (s *loginsScreen) body(height int, focused bool) string {
	if s.err != nil {
		return "mini's MCPs couldn't be read: " + s.err.Error()
	}
	if s.loading() {
		return spinnerFrames[s.spinnerFrame%len(spinnerFrames)] + " Checking how to log in to your MCPs…"
	}
	rows, first, last := s.serverLines(focused)
	lines := slices.Concat(rows, s.detailLines(focused), s.skippedLoginWarning(), s.byHandList())
	return strings.Join(s.scroll.cut(lines, first, last, height), "\n")
}

// The heading calls logins optional, so the screen says what skipping one costs.
func (s *loginsScreen) skippedLoginWarning() []string {
	waiting := slices.DeleteFunc(slices.Clone(s.rows), func(status initcmd.ServerStatus) bool {
		return !s.notLoggedIn(status)
	})
	if len(waiting) == 0 {
		return nil
	}
	text := "⚠ mini can't use these MCPs until you log in to them."
	if len(waiting) == 1 {
		text = "⚠ mini can't use this MCP until you log in to it."
	}
	text += " You can always log in later using " + s.p.steps.login("<name>") + "."
	return append([]string{""}, strings.Split(ansi.Wordwrap(text, max(s.width, 20), ""), "\n")...)
}

// The rows show only names, so the line under them says what the cursor's row connects to. It stays
// when the cursor leaves the rows, blank, so nothing below moves.
func (s *loginsScreen) detailLines(focused bool) []string {
	if !slices.ContainsFunc(s.rows, func(r initcmd.ServerStatus) bool { return r.URL != "" }) {
		return nil
	}
	if !focused || s.cursor < 0 || s.cursor >= len(s.rows) {
		return []string{"", ""}
	}
	r := s.rows[s.cursor]
	parts := slices.DeleteFunc([]string{host(r.URL), s.p.describe(r.URL)}, func(p string) bool { return p == "" })
	if len(parts) == 0 {
		return []string{"", ""}
	}
	return []string{"", "  " + dim.Render(strings.Join(parts, " · "))}
}

func (s *loginsScreen) notLoggedIn(status initcmd.ServerStatus) bool {
	err, tried := s.results[status.Name]
	return status.NeedsLogin() && (!tried || err != nil)
}

func (s *loginsScreen) byHandList() []string {
	if len(s.byHand) == 0 {
		return nil
	}
	var lines []string
	if len(s.rows) > 0 {
		lines = append(lines, "")
	}
	text := "Browser login isn't available for these MCPs, so you will need to configure them manually later:"
	if len(s.byHand) == 1 {
		text = "Browser login isn't available for this MCP, so you will need to configure it manually later:"
	}
	lines = append(lines, strings.Split(ansi.Wordwrap(text, max(s.width, 20), ""), "\n")...)
	width := initcmd.Widest(s.byHand, func(status initcmd.ServerStatus) string { return status.Name })
	for _, status := range s.byHand {
		lines = append(lines, columnLines(status.Name, width, s.p.steps.step(status), s.width)...)
	}
	return lines
}

// first and last bound the cursor's block: its row and the lines under it.
func (s *loginsScreen) serverLines(focused bool) (lines []string, first, last int) {
	width := initcmd.Widest(s.rows, func(status initcmd.ServerStatus) string { return status.Name })
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
		lines = append(lines, "Every MCP works; nothing is left to set up.")
	}
	return lines, first, last
}

func (s *loginsScreen) rowLines(status initcmd.ServerStatus, atCursor bool, width int) []string {
	lines := []string{cursorMark(atCursor) + s.rowText(status, atCursor, width)}
	if s.pending != nil && s.pending.name == status.Name && s.pending.url != "" {
		lines = append(lines, s.urlLines(4+width)...)
	}
	return lines
}

func (s *loginsScreen) rowText(status initcmd.ServerStatus, atCursor bool, width int) string {
	state, hint := s.state(status), ""
	if atCursor {
		hint = s.hint(status)
	}
	if state == "" && hint == "" {
		return status.Name
	}
	if hint != "" && state != "" {
		// A failed login's error can run past the window, and the app would cut the hint after it.
		room := s.width - len("> ") - width - len("  ") - len("  ") - len(hint)
		state = ansi.Truncate(state, max(room, 1), "…") + "  "
	}
	if hint != "" {
		hint = dim.Render(hint)
	}
	return fmt.Sprintf("%-*s  %s%s", width, status.Name, state, hint)
}

func (s *loginsScreen) hint(status initcmd.ServerStatus) string {
	if s.pending != nil {
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

// Authorize URLs run to hundreds of characters, and the app cuts lines at the window's edge.
func (s *loginsScreen) urlLines(indent int) []string {
	var lines []string
	for _, part := range strings.Split(ansi.Hardwrap(s.pending.url, max(s.width-indent, 20), false), "\n") {
		lines = append(lines, strings.Repeat(" ", indent)+ansi.SetHyperlink(s.pending.url)+part+ansi.ResetHyperlink())
	}
	return lines
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
