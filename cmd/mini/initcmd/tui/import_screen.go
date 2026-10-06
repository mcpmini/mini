package tui

import (
	"net/url"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

type importScreen struct {
	candidates []initcmd.Candidate
	agents     []string
	list       *list
}

func newImportScreen(candidates []initcmd.Candidate) *importScreen {
	s := &importScreen{candidates: candidates, agents: agentsOf(candidates)}
	checked := map[string]bool{}
	var rows []row
	widths := s.columnWidths()
	for _, c := range candidates {
		checked[c.Server.Name] = c.Picked
		rows = append(rows, row{
			key:      c.Server.Name,
			label:    c.Server.Name,
			detail:   s.detail(c, widths),
			subtitle: s.unpickedReason(c),
		})
	}
	s.list = newList(rows, checked)
	return s
}

func agentsOf(candidates []initcmd.Candidate) []string {
	var names []string
	for _, c := range candidates {
		for _, from := range c.From {
			if !slices.Contains(names, from.Agent) {
				names = append(names, from.Agent)
			}
		}
	}
	return names
}

func (s *importScreen) heading() string {
	if len(s.agents) == 1 {
		return "Import servers from " + s.agents[0]
	}
	return "Import servers from your agents"
}

func (s *importScreen) handle(key tea.KeyPressMsg) step {
	if s.list.handle(key) {
		return stay
	}
	switch key.String() {
	case "a":
		s.list.toggleAll()
	case "enter":
		return forward
	case "esc", "left", "shift+tab":
		return back
	}
	return stay
}

func (s *importScreen) body(height int) string {
	return s.list.view(height)
}

func (s *importScreen) keys() string {
	return "space tick · a all · / filter · enter continue"
}

func (s *importScreen) empty() bool {
	return len(s.candidates) == 0
}

func (s *importScreen) pick(candidates []initcmd.Candidate) {
	for i := range candidates {
		candidates[i].Picked = s.list.checked[candidates[i].Server.Name]
	}
}

type columns struct {
	target int
	agents int
	auth   int
}

func (s *importScreen) columnWidths() columns {
	var w columns
	for _, c := range s.candidates {
		w.target = max(w.target, ansi.StringWidth(target(c.Server)))
		w.agents = max(w.agents, ansi.StringWidth(agentList(c)))
		w.auth = max(w.auth, ansi.StringWidth(authKind(c.Server)))
	}
	return w
}

// The agents column is dropped when every server comes from one agent: the heading names it.
func (s *importScreen) detail(c initcmd.Candidate, w columns) string {
	parts := []string{pad(target(c.Server), w.target)}
	if len(s.agents) > 1 {
		parts = append(parts, pad(agentList(c), w.agents))
	}
	if w.auth > 0 {
		parts = append(parts, pad(authKind(c.Server), w.auth))
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

// An unticked row says why, so ticking it is an informed choice.
func (s *importScreen) unpickedReason(c initcmd.Candidate) string {
	switch {
	case c.Picked:
		return ""
	case c.Reason == initcmd.SkipSwitchedOff:
		return "switched off in " + agentList(c)
	}
	reason := "another config named " + initcmd.NormalizeName(c.From[0].Name)
	if primary, ok := s.primaryOf(c); ok {
		if differences := agents.ConnectionDifferences(primary.Server, c.Server); len(differences) > 0 {
			reason += ": different " + strings.Join(differences, ", ")
		}
	}
	return reason
}

// Two configs under one name often share a target, so the row says what sets them apart.
func (s *importScreen) primaryOf(second initcmd.Candidate) (initcmd.Candidate, bool) {
	name := initcmd.NormalizeName(second.From[0].Name)
	for _, c := range s.candidates {
		if c.Reason == initcmd.SkipSecondConfig || c.Server.Name == second.Server.Name {
			continue
		}
		for _, from := range c.From {
			if initcmd.NormalizeName(from.Name) == name {
				return c, true
			}
		}
	}
	return initcmd.Candidate{}, false
}

// A long command would crowd out the other columns.
const maxTargetRunes = 40

func target(sc config.ServerConfig) string {
	return ansi.Truncate(fullTarget(sc), maxTargetRunes, "…")
}

func fullTarget(sc config.ServerConfig) string {
	if sc.URL != "" {
		if u, err := url.Parse(sc.URL); err == nil && u.Host != "" {
			return u.Host + strings.TrimSuffix(u.Path, "/")
		}
		return sc.URL
	}
	return strings.TrimSpace(sc.Command + " " + strings.Join(sc.Args, " "))
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(width-ansi.StringWidth(s), 0))
}

func agentList(c initcmd.Candidate) string {
	return strings.Join(agentsOf([]initcmd.Candidate{c}), ", ")
}

func authKind(sc config.ServerConfig) string {
	switch {
	case sc.Auth != nil && sc.Auth.Type == config.AuthTypeOAuth2:
		return "oauth"
	case len(sc.Headers) > 0:
		return "headers"
	}
	return ""
}
