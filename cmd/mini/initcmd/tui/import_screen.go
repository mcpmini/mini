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
	inMini     []initcmd.Candidate
	agents     []string
	list       *list
}

func newImportScreen(plan initcmd.ImportPlan) *importScreen {
	s := &importScreen{candidates: plan.Candidates}
	for _, server := range plan.InMini {
		s.inMini = append(s.inMini, initcmd.Candidate{Server: server.Server, From: server.From})
	}
	s.agents = agentsOf(slices.Concat(s.candidates, s.inMini))
	checked := map[string]bool{}
	widths := s.columnWidths()
	for _, c := range s.candidates {
		checked[c.Server.Name] = c.Picked
	}
	s.list = newList(s.rows(s.candidates, widths), checked)
	s.list.filterable = true
	s.list.untickable, s.list.legend = s.rows(s.inMini, widths), inMiniLegend
	s.list.header = row{label: "SERVER", detail: s.columnText(targetHeading, agentsHeading, widths)}
	return s
}

func (s *importScreen) rows(candidates []initcmd.Candidate, widths columns) []row {
	var rows []row
	for _, c := range candidates {
		rows = append(rows, row{
			key:      c.Server.Name,
			label:    c.Server.Name,
			detail:   s.columnText(target(c.Server), agentList(c), widths),
			subtitle: unpickedReason(c),
		})
	}
	return rows
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

func (s *importScreen) handle(key tea.KeyPressMsg) (reply, tea.Cmd) {
	return s.list.handle(key), nil
}

func (s *importScreen) body(height int, focused bool) string {
	return s.list.view(height, focused)
}

func (s *importScreen) keys() string {
	return s.list.keys("↑↓ move · space/enter tick · a all · tab continue · / filter")
}

func (s *importScreen) takesEsc() bool {
	return s.list.filter.active()
}

func (s *importScreen) focusable() bool {
	return s.list.focusable()
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
	for _, c := range slices.Concat(s.candidates, s.inMini) {
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
