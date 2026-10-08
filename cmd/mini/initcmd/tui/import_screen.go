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
			detail:   s.columnText(target(c.Server), agentList(c), widths),
			subtitle: s.unpickedReason(c),
		})
	}
	s.list = newList(rows, checked)
	s.list.header = row{label: "SERVER", detail: s.columnText(targetHeading, agentsHeading, widths)}
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
	return s.list.keys("space tick · a all · / filter · enter continue")
}

func (s *importScreen) filterLine() string {
	return s.list.filterLine()
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
}

const targetHeading, agentsHeading = "COMMAND / URL", "FROM"

func (s *importScreen) columnWidths() columns {
	w := columns{target: ansi.StringWidth(targetHeading), agents: ansi.StringWidth(agentsHeading)}
	for _, c := range s.candidates {
		w.target = max(w.target, ansi.StringWidth(target(c.Server)))
		w.agents = max(w.agents, ansi.StringWidth(agentList(c)))
	}
	return w
}

// The agents column is dropped when every server comes from one agent: the heading names it.
func (s *importScreen) columnText(target, agents string, w columns) string {
	parts := []string{pad(target, w.target)}
	if len(s.agents) > 1 {
		parts = append(parts, pad(agents, w.agents))
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func (s *importScreen) unpickedReason(c initcmd.Candidate) string {
	switch {
	case c.Picked:
		return ""
	case c.Reason == initcmd.SkipSwitchedOff:
		return "switched off in " + agentList(c)
	}
	reason := "another config named " + c.SharesName
	if primary, ok := s.named(c.SharesName); ok {
		// Two configs under one name often share a target, so the row says what sets them apart.
		if differences := agents.ConnectionDifferences(primary.Server, c.Server); len(differences) > 0 {
			reason += ": different " + strings.Join(differences, ", ")
		}
	}
	return reason
}

func (s *importScreen) named(name string) (initcmd.Candidate, bool) {
	i := slices.IndexFunc(s.candidates, func(c initcmd.Candidate) bool { return c.Server.Name == name })
	if i < 0 {
		return initcmd.Candidate{}, false
	}
	return s.candidates[i], true
}

const maxTargetWidth = 40

func target(sc config.ServerConfig) string {
	return ansi.Truncate(fullTarget(sc), maxTargetWidth, "…")
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
