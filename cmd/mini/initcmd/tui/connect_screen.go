package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
)

// connectPlan is initcmd.ConnectPlan; tests swap it for one whose checks they control.
type connectPlan interface {
	HasDuplicates(agent string) bool
	Check(ctx context.Context) initcmd.Removals
}

type connectParams struct {
	agents []agents.Agent
	// withMini names the agents that already have a mini entry, which connecting leaves as it is.
	withMini map[string]bool
	plan     func() (connectPlan, error)
}

type connectScreen struct {
	p    connectParams
	plan connectPlan
	// listed are the agents connecting can change; noted already have mini and nothing to remove.
	listed []agents.Agent
	noted  []string
	ticked map[string]bool
	cursor int
	chosen initcmd.ConnectChoice
	checks connectChecks
}

func newConnectScreen(p connectParams) *connectScreen {
	return &connectScreen{p: p, ticked: map[string]bool{}}
}

// Servers saved or logged in to since the last visit change what removing would do, so the
// agents are read again each time.
func (s *connectScreen) rebuild() {
	plan, err := s.p.plan()
	if err != nil {
		// Apply loads the same servers and reports the error for each agent; until then nothing is removable.
		plan = initcmd.ConnectPlan{}
	}
	s.plan = plan
	s.listed, s.noted = nil, nil
	for _, agent := range s.p.agents {
		if s.p.withMini[agent.Name] && !s.plan.HasDuplicates(agent.Name) {
			s.noted = append(s.noted, agent.Name)
			continue
		}
		if _, seen := s.ticked[agent.Name]; !seen {
			s.ticked[agent.Name] = true
		}
		s.listed = append(s.listed, agent)
	}
}

func (s *connectScreen) enter() tea.Cmd {
	s.rebuild()
	s.cursor = s.agentRows()
	return s.checks.start(s.plan)
}

func (s *connectScreen) start() tea.Cmd {
	return nil
}

func (s *connectScreen) update(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(connectChecked); ok {
		s.checks.finished(msg)
	}
	return nil
}

func (s *connectScreen) options() []initcmd.ConnectChoice {
	if s.removable() {
		return []initcmd.ConnectChoice{initcmd.ConnectAndRemove, initcmd.ConnectOnly, initcmd.DontConnect}
	}
	return []initcmd.ConnectChoice{initcmd.ConnectOnly, initcmd.DontConnect}
}

func (s *connectScreen) removable() bool {
	for _, agent := range s.listed {
		if s.plan.HasDuplicates(agent.Name) {
			return true
		}
	}
	return false
}

func (s *connectScreen) agentRows() int {
	if len(s.listed) > 1 {
		return len(s.listed)
	}
	return 0
}

func (s *connectScreen) heading() string {
	if len(s.listed) == 1 {
		return "Connect mini to " + s.listed[0].Name
	}
	return "Connect mini to your agents"
}

func (s *connectScreen) handle(key tea.KeyPressMsg) (step, tea.Cmd) {
	switch key.String() {
	case "up":
		s.cursor = max(s.cursor-1, 0)
	case "down":
		s.cursor = min(s.cursor+1, s.agentRows()+len(s.options())-1)
	case "space":
		if s.cursor < s.agentRows() {
			name := s.listed[s.cursor].Name
			s.ticked[name] = !s.ticked[name]
		}
	case "enter":
		return s.choose()
	case "esc", "left", "shift+tab":
		s.checks.cancel()
		return back, nil
	}
	return stay, nil
}

func (s *connectScreen) choose() (step, tea.Cmd) {
	if s.cursor < s.agentRows() {
		s.cursor = s.agentRows()
		return stay, nil
	}
	choice := s.options()[s.cursor-s.agentRows()]
	if choice == initcmd.ConnectAndRemove && !s.checks.done {
		return stay, nil
	}
	s.checks.cancel()
	s.chosen = choice
	return forward, nil
}

func (s *connectScreen) body(int) string {
	var lines []string
	if len(s.noted) > 0 {
		lines = append(lines, dim.Render(alreadyHaveMini(s.noted)), "")
	}
	for i := range s.agentRows() {
		lines = append(lines, cursorMark(i == s.cursor)+checkbox(s.ticked[s.listed[i].Name])+s.listed[i].Name)
	}
	if s.agentRows() > 0 {
		lines = append(lines, "")
	}
	for i, choice := range s.options() {
		lines = append(lines, cursorMark(s.cursor == s.agentRows()+i)+optionLabel(choice))
		for _, subtitle := range s.subtitles(choice) {
			lines = append(lines, "    "+dim.Render(subtitle))
		}
	}
	return strings.Join(lines, "\n")
}

func optionLabel(choice initcmd.ConnectChoice) string {
	switch choice {
	case initcmd.ConnectAndRemove:
		return "Connect mini and remove existing MCPs"
	case initcmd.ConnectOnly:
		return "Just connect mini"
	}
	return "Don't connect"
}

func (s *connectScreen) subtitles(choice initcmd.ConnectChoice) []string {
	switch choice {
	case initcmd.ConnectAndRemove:
		return s.removeSubtitles()
	case initcmd.ConnectOnly:
		return []string{"Adds mini next to your existing MCPs"}
	}
	var names []string
	for _, agent := range s.listed {
		names = append(names, agent.Name)
	}
	return []string{"Leaves " + withVerb(names, "as it is", "as they are")}
}

func (s *connectScreen) removeSubtitles() []string {
	if !s.checks.done {
		return []string{"checking servers…"}
	}
	removed, disables := 0, false
	for _, agent := range s.picked() {
		removed += len(s.checks.removals.ByAgent[agent.Name])
		disables = disables || initcmd.RemovalDisables(agent)
	}
	lines := []string{fmt.Sprintf("Will remove %d %s from existing agent configs. "+
		"They will be backed up alongside the existing files with minibackup.<ext>", removed, mcps(removed))}
	if disables {
		lines = append(lines, "Codex: existing MCPs will be disabled, not removed")
	}
	return lines
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

func mcps(n int) string {
	if n == 1 {
		return "MCP"
	}
	return "MCPs"
}

func (s *connectScreen) keys() string {
	if s.cursor < s.agentRows() {
		return "space tick · ↑↓ move · enter continue"
	}
	return "↑↓ move · enter choose"
}

func (s *connectScreen) empty() bool {
	s.rebuild()
	return len(s.listed) == 0
}

func (s *connectScreen) picked() []agents.Agent {
	var picked []agents.Agent
	for _, agent := range s.listed {
		if s.ticked[agent.Name] {
			picked = append(picked, agent)
		}
	}
	return picked
}

func (s *connectScreen) connectParams() initcmd.ConnectParams {
	return initcmd.ConnectParams{Agents: s.picked(), Choice: s.chosen, Removals: s.checks.removals}
}
