package tui

import (
	"context"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
)

type connectPlan interface {
	HasDuplicates(agent string) bool
	Check(ctx context.Context) initcmd.Removals
}

type connectParams struct {
	agents   []agents.Agent
	withMini map[string]bool
	plan     func() (connectPlan, error)
}

type connectScreen struct {
	p                connectParams
	plan             connectPlan
	listed           []agents.Agent
	alreadyConnected []string
	ticked           map[string]bool
	// agents ticks the agents to connect when there are several, and marks those already connected.
	agents *list
	chosen initcmd.ConnectChoice
	checks connectChecks
	width  int
}

func newConnectScreen(p connectParams) *connectScreen {
	return &connectScreen{p: p, ticked: map[string]bool{}, agents: newList(nil, nil)}
}

func (s *connectScreen) refresh() {
	plan, err := s.p.plan()
	if err != nil {
		// Apply loads the same servers and reports the error for each agent; until then nothing is removable.
		plan = initcmd.ConnectPlan{}
	}
	s.plan = plan
	s.listed, s.alreadyConnected = nil, nil
	for _, agent := range s.p.agents {
		if s.p.withMini[agent.Name] && !s.plan.HasDuplicates(agent.Name) {
			s.alreadyConnected = append(s.alreadyConnected, agent.Name)
			continue
		}
		if _, seen := s.ticked[agent.Name]; !seen {
			s.ticked[agent.Name] = true
		}
		s.listed = append(s.listed, agent)
	}
	var rows, connected []row
	if len(s.listed) > 1 {
		for _, agent := range s.listed {
			rows = append(rows, row{key: agent.Name, label: agent.Name})
		}
	}
	for _, name := range s.alreadyConnected {
		connected = append(connected, row{key: name, label: name, detail: "already connected"})
	}
	s.agents = newList(rows, s.ticked)
	s.agents.untickable = connected
}

func (s *connectScreen) enter() tea.Cmd {
	// The app starts the cursor on the first choice, so up from it goes to the last row.
	s.agents.cursor, s.agents.scroll = max(s.agents.cursorRows()-1, 0), scroll{}
	return s.checks.start(s.plan)
}

func (s *connectScreen) leave() {
	s.checks.cancel()
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
	return slices.ContainsFunc(s.listed, func(agent agents.Agent) bool { return s.plan.HasDuplicates(agent.Name) })
}

func (s *connectScreen) heading() string {
	if len(s.listed) == 1 {
		return "Connect mini to " + s.listed[0].Name
	}
	return "Connect mini to your agents"
}

func (s *connectScreen) handle(key tea.KeyPressMsg) (reply, tea.Cmd) {
	return s.agents.handle(key), nil
}

func (s *connectScreen) focusable() bool {
	return s.agents.focusable()
}

func (s *connectScreen) choices() []string {
	var labels []string
	for _, choice := range s.options() {
		labels = append(labels, optionLabel(choice))
	}
	return labels
}

func (s *connectScreen) choiceLines(choice int) []string {
	var lines []string
	for _, subtitle := range s.subtitles(s.options()[choice]) {
		lines = append(lines, s.subtitleLines(subtitle)...)
	}
	return lines
}

func (s *connectScreen) choose(i int) (chosen bool) {
	choice := s.options()[i]
	// Removing needs the checks' results; until they arrive its lines say it is checking.
	if choice == initcmd.ConnectAndRemove && !s.checks.done {
		return false
	}
	if choice != initcmd.DontConnect && len(s.picked()) == 0 {
		return false
	}
	s.chosen = choice
	return true
}

func (s *connectScreen) body(height int, focused bool) string {
	if !s.focusable() {
		return ""
	}
	return s.agents.view(height, focused)
}

// Subtitles run past a narrow window, and the app cuts lines at its edge, so they wrap.
func (s *connectScreen) subtitleLines(subtitle string) []string {
	var lines []string
	for _, line := range strings.Split(ansi.Wrap(subtitle, max(s.width-2, 20), ""), "\n") {
		lines = append(lines, "  "+dim.Render(line))
	}
	return lines
}

func (s *connectScreen) resize(width, _ int) {
	s.width = width
}

func optionLabel(choice initcmd.ConnectChoice) string {
	switch choice {
	case initcmd.ConnectAndRemove:
		return "Connect mini and remove existing MCPs"
	case initcmd.ConnectOnly:
		return "Just connect mini"
	}
	return "I'll connect mini later"
}

func (s *connectScreen) subtitles(choice initcmd.ConnectChoice) []string {
	if choice != initcmd.DontConnect && len(s.picked()) == 0 {
		return []string{"Tick the agents above to connect mini to them"}
	}
	switch choice {
	case initcmd.ConnectAndRemove:
		return s.removeSubtitles()
	case initcmd.ConnectOnly:
		return s.connectOnlySubtitles()
	}
	return []string{"Leaves " + withVerb(agentNames(s.listed), "as it is", "as they are")}
}

// An agent listed only for its removable MCPs already has mini, so just connecting it changes nothing.
func (s *connectScreen) connectOnlySubtitles() []string {
	names := agentNames(s.picked())
	lacksMini := func(name string) bool { return !s.p.withMini[name] }
	if slices.ContainsFunc(names, lacksMini) {
		return []string{"Adds mini next to your existing MCPs"}
	}
	return []string{"Changes nothing: " + alreadyHaveMini(names)}
}

func agentNames(list []agents.Agent) []string {
	var names []string
	for _, agent := range list {
		names = append(names, agent.Name)
	}
	return names
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
		return []string{"Nothing to remove: none of your MCPs work in mini yet"}
	}
	lines := []string{
		"Removes " + initcmd.Plural(removed, "MCP") + " that mini now runs; the configs are backed up first",
	}
	for _, name := range disabling {
		lines = append(lines, name+": existing MCPs will be disabled, not removed")
	}
	return lines
}

func (s *connectScreen) anyListedHasRemovals() bool {
	return slices.ContainsFunc(s.listed, func(agent agents.Agent) bool {
		return len(s.checks.removals.ByAgent[agent.Name]) > 0
	})
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
	if len(s.agents.rows) == 0 {
		return "↑↓ move · tab choices"
	}
	return "↑↓ move · space/enter tick · a all · tab choices"
}

func (s *connectScreen) empty() bool {
	return len(s.listed) == 0
}

func (s *connectScreen) picked() []agents.Agent {
	// A lone agent has no checkbox, so a tick it lost while it had one can't be put back.
	if len(s.listed) == 1 {
		return s.listed
	}
	var picked []agents.Agent
	for _, agent := range s.listed {
		if s.ticked[agent.Name] {
			picked = append(picked, agent)
		}
	}
	return picked
}

func (s *connectScreen) chosenConnect() initcmd.ConnectParams {
	return initcmd.ConnectParams{Agents: s.picked(), Choice: s.chosen, Removals: s.checks.removals}
}
