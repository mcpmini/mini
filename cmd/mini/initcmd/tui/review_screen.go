package tui

import (
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type reviewParams struct {
	added     func() []string
	configDir string
	// earlier are the screens Review follows; while one of them still shows, Review is skipped.
	earlier []screen
	// offered are the screens that offer servers; when none offers any, there is nothing to review.
	offered []screen
}

// reviewScreen is the last screen of a run that doesn't connect an agent. It has no rows of its own.
type reviewScreen struct {
	p     reviewParams
	width int
}

func newReviewScreen(p reviewParams) *reviewScreen {
	return &reviewScreen{p: p}
}

func (s *reviewScreen) heading() string {
	return "Review and finish"
}

func (s *reviewScreen) body(int, bool) string {
	if added := s.p.added(); len(added) > 0 {
		return strings.Join(addedNote(added, s.width), "\n")
	}
	servers := filepath.Join(s.p.configDir, "servers")
	lines := []string{"No MCPs will be added to mini.", ""}
	guidance := "Go back to pick some, or add them later with mini add or by writing their config files in " +
		servers + "."
	lines = append(lines, strings.Split(ansi.Wrap(guidance, max(s.width, 20), ""), "\n")...)
	return strings.Join(lines, "\n")
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
	return anyShown(s.p.earlier) || !anyShown(s.p.offered)
}

func anyShown(list []screen) bool {
	return slices.ContainsFunc(list, func(s screen) bool { return !s.empty() })
}
