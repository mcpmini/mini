package tui

import (
	"net/url"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
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
			subtitle: unpickedReason(c),
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

func (s *importScreen) handle(key tea.KeyPressMsg) (step, tea.Cmd) {
	if s.list.handle(key) {
		return stay, nil
	}
	switch key.String() {
	case "a":
		s.list.toggleAll()
	case "enter":
		return forward, nil
	case "esc", "left", "shift+tab":
		return back, nil
	}
	return stay, nil
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

func (s *importScreen) ticked() []config.ServerConfig {
	var servers []config.ServerConfig
	for _, c := range s.candidates {
		if s.list.checked[c.Server.Name] {
			servers = append(servers, c.Server)
		}
	}
	return servers
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

func unpickedReason(c initcmd.Candidate) string {
	if !c.Picked && c.Reason == initcmd.SkipSwitchedOff {
		return "switched off in " + agentList(c)
	}
	return ""
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
