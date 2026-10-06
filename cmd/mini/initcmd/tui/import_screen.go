package tui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

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
		rows = append(rows, row{key: c.Server.Name, label: c.Server.Name, detail: s.detail(c, widths)})
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
}

func (s *importScreen) columnWidths() columns {
	var w columns
	for _, c := range s.candidates {
		w.target = max(w.target, len(target(c.Server)))
		w.agents = max(w.agents, len(agentList(c)))
	}
	return w
}

// The agents column is dropped when every server comes from one agent: the heading names it.
func (s *importScreen) detail(c initcmd.Candidate, w columns) string {
	parts := []string{fmt.Sprintf("%-*s", w.target, target(c.Server))}
	if len(s.agents) > 1 {
		parts = append(parts, fmt.Sprintf("%-*s", w.agents, agentList(c)))
	}
	parts = append(parts, authKind(c.Server))
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func target(sc config.ServerConfig) string {
	if sc.URL != "" {
		if u, err := url.Parse(sc.URL); err == nil && u.Host != "" {
			return u.Host
		}
		return sc.URL
	}
	return strings.TrimSpace(sc.Command + " " + strings.Join(sc.Args, " "))
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
