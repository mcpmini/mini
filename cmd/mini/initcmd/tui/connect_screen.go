package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
)

type connectOption struct {
	choice initcmd.ConnectChoice
	label  string
}

var connectOptions = []connectOption{
	{initcmd.ConnectOnly, "Just connect mini"},
	{initcmd.DontConnect, "Don't connect"},
}

type connectScreen struct {
	agents   []agents.Agent
	withMini []string
	ticked   map[string]bool
	cursor   int
	chosen   initcmd.ConnectChoice
}

func newConnectScreen(list []agents.Agent, withMini map[string]bool) *connectScreen {
	s := &connectScreen{ticked: map[string]bool{}}
	for _, agent := range list {
		if withMini[agent.Name] {
			s.withMini = append(s.withMini, agent.Name)
			continue
		}
		s.agents = append(s.agents, agent)
		s.ticked[agent.Name] = true
	}
	s.cursor = s.agentRows()
	return s
}

func (s *connectScreen) agentRows() int {
	if len(s.agents) > 1 {
		return len(s.agents)
	}
	return 0
}

func (s *connectScreen) heading() string {
	if len(s.agents) == 1 {
		return "Connect mini to " + s.agents[0].Name
	}
	return "Connect mini to your agents"
}

func (s *connectScreen) handle(key tea.KeyPressMsg) (step, tea.Cmd) {
	switch key.String() {
	case "up":
		s.cursor = max(s.cursor-1, 0)
	case "down":
		s.cursor = min(s.cursor+1, s.agentRows()+len(connectOptions)-1)
	case "space":
		if s.cursor < s.agentRows() {
			name := s.agents[s.cursor].Name
			s.ticked[name] = !s.ticked[name]
		}
	case "enter":
		if s.cursor < s.agentRows() {
			s.cursor = s.agentRows()
			return stay, nil
		}
		s.chosen = connectOptions[s.cursor-s.agentRows()].choice
		return forward, nil
	case "esc", "left", "shift+tab":
		return back, nil
	}
	return stay, nil
}

func (s *connectScreen) body(int) string {
	var lines []string
	if len(s.withMini) > 0 {
		lines = append(lines, dim.Render(alreadyHaveMini(s.withMini)), "")
	}
	for i := range s.agentRows() {
		lines = append(lines, cursorMark(i == s.cursor)+checkbox(s.ticked[s.agents[i].Name])+s.agents[i].Name)
	}
	if s.agentRows() > 0 {
		lines = append(lines, "")
	}
	for i, option := range connectOptions {
		lines = append(
			lines,
			cursorMark(s.cursor == s.agentRows()+i)+option.label,
			"    "+dim.Render(s.subtitle(option.choice)),
		)
	}
	return strings.Join(lines, "\n")
}

func (s *connectScreen) subtitle(choice initcmd.ConnectChoice) string {
	if choice == initcmd.ConnectOnly {
		return "Adds mini next to your existing MCPs"
	}
	var names []string
	for _, agent := range s.agents {
		names = append(names, agent.Name)
	}
	return "Leaves " + withVerb(names, "as it is", "as they are")
}

func alreadyHaveMini(names []string) string {
	return withVerb(names, "already has a mini entry.", "already have a mini entry.")
}

func withVerb(names []string, one, many string) string {
	if len(names) == 1 {
		return names[0] + " " + one
	}
	return initcmd.JoinAnd(names) + " " + many
}

func (s *connectScreen) keys() string {
	if s.cursor < s.agentRows() {
		return "space tick · ↑↓ move · enter continue"
	}
	return "↑↓ move · enter choose"
}

func (s *connectScreen) empty() bool {
	return len(s.agents) == 0
}

func (s *connectScreen) picked() []agents.Agent {
	var picked []agents.Agent
	for _, agent := range s.agents {
		if s.ticked[agent.Name] {
			picked = append(picked, agent)
		}
	}
	return picked
}
