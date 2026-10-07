package tui

import (
	"fmt"
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

// connectScreen asks which agents to connect mini to, and how. The cursor moves over the agent
// rows, shown only when there is more than one agent, then the options.
type connectScreen struct {
	agents  []agents.Agent
	running map[string]bool
	ticked  map[string]bool
	cursor  int
	chosen  initcmd.ConnectChoice
}

// running names the agents whose mini entry already serves this config directory.
func newConnectScreen(list []agents.Agent, running map[string]bool) *connectScreen {
	s := &connectScreen{agents: list, running: running, ticked: map[string]bool{}}
	for _, agent := range list {
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
	width := 0
	for _, agent := range s.agents {
		width = max(width, len(agent.Name))
	}
	for i := range s.agentRows() {
		lines = append(lines, s.cursorMark(i)+s.agentLine(s.agents[i], width))
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	for i, option := range connectOptions {
		lines = append(lines, s.cursorMark(s.agentRows()+i)+option.label, "    "+dim.Render(s.subtitle(option.choice)))
	}
	return strings.Join(lines, "\n")
}

func (s *connectScreen) agentLine(agent agents.Agent, width int) string {
	box := "[ ]"
	if s.ticked[agent.Name] {
		box = "[x]"
	}
	detail := agent.ConfigPath
	if s.running[agent.Name] {
		detail = "already runs mini"
	}
	return fmt.Sprintf("%s %-*s  %s", box, width, agent.Name, dim.Render(detail))
}

func (s *connectScreen) cursorMark(i int) string {
	if i == s.cursor {
		return "> "
	}
	return "  "
}

func (s *connectScreen) subtitle(choice initcmd.ConnectChoice) string {
	if choice == initcmd.ConnectOnly {
		return "Adds mini next to your existing MCPs"
	}
	var names []string
	for _, agent := range s.picked() {
		names = append(names, agent.Name)
	}
	switch len(names) {
	case 0:
		return "Leaves your agents as they are"
	case 1:
		return "Leaves " + names[0] + " as it is"
	}
	return "Leaves " + initcmd.JoinAnd(names) + " as they are"
}

func (s *connectScreen) keys() string {
	if s.cursor < s.agentRows() {
		return "space tick · ↑↓ move · enter continue"
	}
	return "↑↓ move · enter choose"
}

// Connecting has nothing to do when every agent already runs mini.
func (s *connectScreen) empty() bool {
	for _, agent := range s.agents {
		if !s.running[agent.Name] {
			return false
		}
	}
	return true
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
