package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
	width  int
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
	choice, onOption := s.highlighted()
	if !onOption {
		s.cursor = s.agentRows()
		return stay, nil
	}
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
			lines = append(lines, s.subtitleLines(subtitle)...)
		}
	}
	return strings.Join(lines, "\n")
}

// Subtitles run past a narrow window, and the app cuts lines at its edge, so they wrap.
func (s *connectScreen) subtitleLines(subtitle string) []string {
	var lines []string
	for _, line := range strings.Split(ansi.Wrap(subtitle, max(s.width-4, 20), ""), "\n") {
		lines = append(lines, "    "+dim.Render(line))
	}
	return lines
}

func (s *connectScreen) resize(width int) {
	s.width = width
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
		return s.connectOnlySubtitles()
	}
	var names []string
	for _, agent := range s.listed {
		names = append(names, agent.Name)
	}
	return []string{"Leaves " + withVerb(names, "as it is", "as they are")}
}

// An agent listed only for its removable MCPs already has mini, so just connecting it changes nothing.
func (s *connectScreen) connectOnlySubtitles() []string {
	var withMini []string
	for _, agent := range s.picked() {
		if !s.p.withMini[agent.Name] {
			return []string{"Adds mini next to your existing MCPs"}
		}
		withMini = append(withMini, agent.Name)
	}
	if len(withMini) == 0 {
		return []string{"Adds mini next to your existing MCPs"}
	}
	return []string{"Changes nothing: " + alreadyHaveMini(withMini)}
}

func (s *connectScreen) removeSubtitles() []string {
	if !s.checks.done {
		return []string{"checking servers…"}
	}
	removed := 0
	var disabling []string
	for _, agent := range s.picked() {
		entries := len(s.checks.removals.ByAgent[agent.Name])
		removed += entries
		if entries > 0 && agent.RemoveDisables {
			disabling = append(disabling, agent.Name)
		}
	}
	if removed == 0 && s.anyListedHasRemovals() {
		return []string{"Nothing to remove from the ticked agents"}
	}
	if removed == 0 {
		return []string{"Nothing to remove yet: no existing MCP's mini copy passed its connection check"}
	}
	lines := []string{fmt.Sprintf("Will remove %d %s from existing agent configs. "+
		"They will be backed up alongside the existing files with minibackup.<ext>", removed, mcps(removed))}
	for _, name := range disabling {
		lines = append(lines, name+": existing MCPs will be disabled, not removed")
	}
	return lines
}

func (s *connectScreen) anyListedHasRemovals() bool {
	for _, agent := range s.listed {
		if len(s.checks.removals.ByAgent[agent.Name]) > 0 {
			return true
		}
	}
	return false
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
	choice, onOption := s.highlighted()
	switch {
	case !onOption:
		return "space tick · ↑↓ move · enter continue"
	case choice == initcmd.ConnectAndRemove && !s.checks.done:
		return "↑↓ move · removing waits for the server checks"
	}
	return "↑↓ move · enter choose"
}

func (s *connectScreen) highlighted() (choice initcmd.ConnectChoice, onOption bool) {
	if s.cursor < s.agentRows() {
		return choice, false
	}
	return s.options()[s.cursor-s.agentRows()], true
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
