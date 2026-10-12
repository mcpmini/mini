package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

type reviewParams struct {
	added         func() []string
	statuses      func() ([]initcmd.ServerStatus, error)
	checking      func() map[string]bool
	steps         serverSteps
	configDir     string
	endingInstead []screen
	offering      []screen
}

type reviewScreen struct {
	p     reviewParams
	added []reviewRow
	width int
}

type reviewRow struct {
	name, state string
}

func newReviewScreen(p reviewParams) *reviewScreen {
	return &reviewScreen{p: p}
}

func (s *reviewScreen) heading() string {
	return "Review and finish"
}

// Checks before statuses, as Logins reads them: one that finishes in between still reads as running.
func (s *reviewScreen) refresh() {
	checking := s.p.checking()
	//nolint:errcheck // Logins shows the read error; a server missing here still reads as added, with no state.
	statuses, _ := s.p.statuses()
	s.added = nil
	for _, name := range s.p.added() {
		row := reviewRow{name: name}
		if i := slices.IndexFunc(statuses, func(st initcmd.ServerStatus) bool { return st.Name == name }); i >= 0 {
			row.state = s.p.steps.state(statuses[i], checking[name])
		}
		s.added = append(s.added, row)
	}
}

// A check finishing can turn a server from checking to needing a login.
func (s *reviewScreen) update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(checksChanged); ok {
		s.refresh()
	}
	return nil
}

func (s *reviewScreen) body(height int, _ bool) string {
	if len(s.added) > 0 {
		return strings.Join(s.addedLines(height), "\n")
	}
	servers := filepath.Join(s.p.configDir, "servers")
	lines := []string{"No MCPs will be added to mini.", ""}
	guidance := "Go back to pick some, or add them later with mini add or by writing their config files in " +
		servers + "."
	lines = append(lines, strings.Split(ansi.Wrap(guidance, max(s.width, 20), ""), "\n")...)
	return strings.Join(lines, "\n")
}

// Review takes no cursor, so a list taller than the window can't scroll; the servers that don't fit
// are counted on a last line instead, under the heading.
func (s *reviewScreen) addedLines(height int) []string {
	lines := []string{initcmd.Plural(len(s.added), "MCP") + " will be added to mini:"}
	width := widest(s.added, func(r reviewRow) string { return r.name })
	for i, r := range s.added {
		row := columnLines(r.name, width, r.state, s.width)
		left := len(s.added) - i
		if room := height - len(lines); len(row) > room || left > 1 && len(row) == room {
			return append(lines, dim.Render(fmt.Sprintf("  … and %d more", left)))
		}
		lines = append(lines, row...)
	}
	return lines
}

func (s *reviewScreen) handle(tea.KeyPressMsg) (reply, tea.Cmd) {
	return unhandled, nil
}

func (s *reviewScreen) keys() string {
	return ""
}

func (s *reviewScreen) focusable() bool {
	return false
}

func (s *reviewScreen) resize(width, _ int) {
	s.width = width
}

func (s *reviewScreen) empty() bool {
	return anyShown(s.p.endingInstead) || !anyShown(s.p.offering)
}

func anyShown(list []screen) bool {
	return slices.ContainsFunc(list, func(s screen) bool { return !s.empty() })
}

func (s *reviewScreen) arrivalHint() string {
	return "review and finish"
}
