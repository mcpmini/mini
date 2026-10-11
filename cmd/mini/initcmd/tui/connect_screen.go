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

// subtitle is a line under a choice; bold marks the one that must be read before choosing.
type subtitle struct {
	text string
	bold bool
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
	return s.anyHasDuplicates(s.listed)
}

func (s *connectScreen) anyHasDuplicates(list []agents.Agent) bool {
	return slices.ContainsFunc(list, func(agent agents.Agent) bool { return s.plan.HasDuplicates(agent.Name) })
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

func (s *connectScreen) disabled(choice int) bool {
	return s.unavailableBecause(s.options()[choice]) != ""
}

func (s *connectScreen) choiceLines(choice int) []string {
	var lines []string
	for _, sub := range s.subtitles(s.options()[choice]) {
		lines = append(lines, s.subtitleLines(sub)...)
	}
	return lines
}

func (s *connectScreen) choose(i int) (chosen bool) {
	choice := s.options()[i]
	if s.unavailableBecause(choice) != "" || s.waitingOnChecks(choice) {
		return false
	}
	s.chosen = choice
	return true
}

// Removing stays enabled while the checks run, so the cursor can rest on it as the first choice.
func (s *connectScreen) waitingOnChecks(choice initcmd.ConnectChoice) bool {
	return choice == initcmd.ConnectAndRemove && !s.checks.done
}

// unavailableBecause is the line saying why a choice can't be picked yet, or "" when it can be.
func (s *connectScreen) unavailableBecause(choice initcmd.ConnectChoice) string {
	if choice != initcmd.DontConnect && len(s.picked()) == 0 {
		return "Tick the agents above to connect mini to them"
	}
	if choice != initcmd.ConnectAndRemove {
		return ""
	}
	if s.checks.done && !s.anyPickedRemoves() {
		return "None of these MCPs work in mini yet, so nothing will be removed."
	}
	return ""
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
// Styling follows the wrap: ansi.Wrap drops bold from the lines it continues.
func (s *connectScreen) subtitleLines(sub subtitle) []string {
	text := strings.TrimLeft(sub.text, " ")
	indent := "  " + sub.text[:len(sub.text)-len(text)]
	style := dim
	if sub.bold {
		style = bold
	}
	var lines []string
	for _, line := range strings.Split(ansi.Wrap(text, max(s.width-len(indent), 20), ""), "\n") {
		lines = append(lines, indent+style.Render(line))
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

func (s *connectScreen) subtitles(choice initcmd.ConnectChoice) []subtitle {
	if reason := s.unavailableBecause(choice); reason != "" {
		return []subtitle{{text: reason}}
	}
	if s.waitingOnChecks(choice) {
		return []subtitle{{text: "Checking which MCPs mini can run…"}}
	}
	switch choice {
	case initcmd.ConnectAndRemove:
		return append([]subtitle{
			{text: "Adds mini to your agent configurations."},
			{text: "Your existing configurations will be backed up first.", bold: true},
		}, s.removalSubtitles()...)
	case initcmd.ConnectOnly:
		return s.connectOnlySubtitles()
	}
	return []subtitle{{text: "Saves the MCPs to mini and leaves your agent configurations alone."}}
}

func (s *connectScreen) connectOnlySubtitles() []subtitle {
	subtitles := []subtitle{
		{text: "Adds mini to your agent configurations and leaves existing MCP configurations alone."},
	}
	if s.anyHasDuplicates(s.picked()) {
		subtitles = append(subtitles, subtitle{
			text: "⚠ Your agents will see these MCPs twice and may not use mini for smaller responses",
		})
	}
	return subtitles
}

// removalSubtitles names each picked agent that loses entries, padding the names so the verbs line up.
func (s *connectScreen) removalSubtitles() []subtitle {
	var removing []agents.Agent
	for _, agent := range s.picked() {
		if len(s.checks.removals.ByAgent[agent.Name]) > 0 {
			removing = append(removing, agent)
		}
	}
	width := widest(removing, func(agent agents.Agent) string { return agent.Name }) + len(":")
	var subtitles []subtitle
	for _, agent := range removing {
		subtitles = append(subtitles, subtitle{text: "  " + pad(agent.Name+":", width) + " " + s.removalText(agent)})
	}
	return subtitles
}

func (s *connectScreen) removalText(agent agents.Agent) string {
	verb := "Removing "
	if agent.RemoveDisables {
		verb = "Disabling "
	}
	return verb + strings.Join(s.checks.removals.ByAgent[agent.Name], ", ")
}

func (s *connectScreen) anyPickedRemoves() bool {
	return slices.ContainsFunc(s.picked(), func(agent agents.Agent) bool {
		return len(s.checks.removals.ByAgent[agent.Name]) > 0
	})
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

func (s *connectScreen) arrivalHint() string {
	return "add mini to your agents"
}
