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
	added    func() []string
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
	return slices.ContainsFunc(s.picked(), func(agent agents.Agent) bool { return s.plan.HasDuplicates(agent.Name) })
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
	note := s.addedLines()
	if len(note) > 0 && s.focusable() {
		note = append(note, "")
	}
	if !s.focusable() {
		return strings.Join(note, "\n")
	}
	return strings.Join(append(note, s.agents.view(max(height-len(note), 1), focused)), "\n")
}

func (s *connectScreen) addedLines() []string {
	added := s.p.added()
	if len(added) == 0 {
		return nil
	}
	text := initcmd.Plural(len(added), "MCP") + " will be added to mini: " + strings.Join(added, ", ")
	return strings.Split(ansi.Wrap(text, max(s.width, 20), ""), "\n")
}

// Subtitles run past a narrow window, and the app cuts lines at its edge, so they wrap.
func (s *connectScreen) subtitleLines(subtitle string) []string {
	text := strings.TrimLeft(subtitle, " ")
	indent := "  " + subtitle[:len(subtitle)-len(text)]
	var lines []string
	for _, line := range strings.Split(ansi.Wrap(text, max(s.width-len(indent), 20), ""), "\n") {
		lines = append(lines, indent+dim.Render(line))
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
		if !s.checks.done {
			return []string{"checking servers…"}
		}
		if !s.anyPickedRemoves() {
			return []string{"Nothing to remove: none of these MCPs work in mini yet"}
		}
		return append([]string{"The configs are backed up first"}, s.tickedAgentLines(s.removeLine)...)
	case initcmd.ConnectOnly:
		return s.tickedAgentLines(s.connectLine)
	}
	return []string{"Leaves " + withVerb(agentNames(s.listed), "as it is", "as they are")}
}

func (s *connectScreen) tickedAgentLines(line func(agent agents.Agent) string) []string {
	var says []string
	alike := map[string][]string{}
	for _, agent := range s.picked() {
		text := line(agent)
		if _, seen := alike[text]; !seen {
			says = append(says, text)
		}
		alike[text] = append(alike[text], agent.Name)
	}
	var lines []string
	for _, text := range says {
		lines = append(lines, "  "+initcmd.JoinAnd(alike[text])+": "+text)
	}
	return lines
}

func (s *connectScreen) anyPickedRemoves() bool {
	return slices.ContainsFunc(s.picked(), func(agent agents.Agent) bool {
		return len(s.checks.removals.ByAgent[agent.Name]) > 0
	})
}

func (s *connectScreen) removeLine(agent agents.Agent) string {
	entries := s.checks.removals.ByAgent[agent.Name]
	verb := "removing "
	if agent.RemoveDisables {
		verb = "disabling "
	}
	hasMini := s.p.withMini[agent.Name]
	switch {
	case len(entries) > 0 && hasMini:
		return verb + strings.Join(entries, ", ")
	case len(entries) > 0:
		return "adding mini, " + verb + strings.Join(entries, ", ")
	case hasMini:
		return "mini already connected, nothing to remove"
	}
	return "adding mini, nothing to remove"
}

func (s *connectScreen) connectLine(agent agents.Agent) string {
	if s.p.withMini[agent.Name] {
		return "mini already connected, nothing changes"
	}
	return "adding mini, leaving existing MCPs"
}

func agentNames(list []agents.Agent) []string {
	var names []string
	for _, agent := range list {
		names = append(names, agent.Name)
	}
	return names
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
