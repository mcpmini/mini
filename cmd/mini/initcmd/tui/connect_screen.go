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
	// cursor is the agent under the cursor until it reaches the choices, which are the screen's actions.
	cursor  int
	actions actions
	chosen  initcmd.ConnectChoice
	checks  connectChecks
	width   int
	scroll  scroll
}

func newConnectScreen(p connectParams) *connectScreen {
	return &connectScreen{p: p, ticked: map[string]bool{}, actions: newActions()}
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
	var labels []string
	for _, choice := range s.options() {
		labels = append(labels, optionLabel(choice))
	}
	s.actions.setChoices(labels)
}

func (s *connectScreen) enter() tea.Cmd {
	// The cursor starts on the first choice, so up from it goes to the last agent.
	s.cursor, s.scroll = max(s.agentRows()-1, 0), scroll{}
	s.actions.reach()
	return s.checks.start(s.plan)
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

func (s *connectScreen) offerBack(back bool) {
	s.actions.offerBack(back)
}

func (s *connectScreen) handle(key tea.KeyPressMsg) (step, tea.Cmd) {
	switch key.String() {
	case "up", "down":
		s.move(direction(key.String()))
	case "tab":
		s.actions.reach()
	case "space":
		s.tick()
	case "a":
		s.tickAll()
	case "enter":
		return s.choose()
	case "esc":
		return s.goBack()
	}
	return stay, nil
}

// Going back cancels the server checks, which only a later visit restarts, so with nowhere to go
// back to they keep running.
func (s *connectScreen) goBack() (step, tea.Cmd) {
	if !s.actions.back {
		return stay, nil
	}
	s.checks.cancel()
	return back, nil
}

// move goes from the last agent to the choices and back; with one agent there are only the choices.
func (s *connectScreen) move(step int) {
	switch {
	case s.agentRows() == 0:
		s.actions.moveWithin(step)
	case s.actions.active:
		s.actions.move(step)
	case step > 0 && s.cursor == s.agentRows()-1:
		s.actions.reach()
	default:
		s.cursor = max(s.cursor+step, 0)
	}
}

func (s *connectScreen) tick() {
	if !s.actions.active {
		name := s.listed[s.cursor].Name
		s.ticked[name] = !s.ticked[name]
	}
}

func (s *connectScreen) tickAll() {
	// With one agent there are no checkboxes, so there is nothing the user could see change.
	if s.agentRows() == 0 {
		return
	}
	all := !slices.ContainsFunc(s.listed, func(agent agents.Agent) bool { return !s.ticked[agent.Name] })
	for _, agent := range s.listed {
		s.ticked[agent.Name] = !all
	}
}

func (s *connectScreen) choose() (step, tea.Cmd) {
	if s.actions.onBack() {
		return s.goBack()
	}
	choice, onOption := s.highlighted()
	if !onOption {
		s.tick()
		return stay, nil
	}
	if choice == initcmd.ConnectAndRemove && !s.checks.done {
		return stay, nil
	}
	s.checks.cancel()
	s.chosen = choice
	return forward, nil
}

// The cursor never reaches the note, so it stays above the scrolled rows instead of scrolling
// away for good on a short window.
func (s *connectScreen) body(height int) string {
	header := s.noteLines()
	if height > 0 {
		header = header[:min(len(header), height-1)]
		height -= len(header)
	}
	return strings.Join(append(header, s.rowLines(height)...), "\n")
}

func (s *connectScreen) rowLines(height int) []string {
	lines, first := s.agentLines()
	last := first
	actions, actionFirst, actionLast := s.actions.lines(s.choiceSubtitles)
	if s.agentRows() == 0 {
		// With one agent the choices open the screen; the blank line would only push them down.
		actions, actionFirst, actionLast = actions[1:], actionFirst-1, actionLast-1
	}
	if s.actions.active {
		first, last = len(lines)+actionFirst, len(lines)+actionLast
	}
	return s.scroll.cut(append(lines, actions...), first, last, height)
}

func (s *connectScreen) choiceSubtitles(choice int) []string {
	var lines []string
	for _, subtitle := range s.subtitles(s.options()[choice]) {
		lines = append(lines, s.subtitleLines(subtitle)...)
	}
	return lines
}

func (s *connectScreen) noteLines() []string {
	if len(s.alreadyConnected) == 0 {
		return nil
	}
	return []string{dim.Render(alreadyHaveMini(s.alreadyConnected)), ""}
}

func (s *connectScreen) agentLines() (lines []string, cursorLine int) {
	for i := range s.agentRows() {
		onAgent := !s.actions.active && i == s.cursor
		if onAgent {
			cursorLine = len(lines)
		}
		lines = append(lines, cursorMark(onAgent)+checkbox(s.ticked[s.listed[i].Name])+s.listed[i].Name)
	}
	return lines, cursorLine
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
	return "Don't connect"
}

func (s *connectScreen) subtitles(choice initcmd.ConnectChoice) []string {
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
	if len(names) == 0 || slices.ContainsFunc(names, lacksMini) {
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
	if !s.actions.active {
		return "↑↓ move · space/enter tick · a all · tab choices"
	}
	keys := s.actions.keys(s.agentRows() > 0)
	if choice, onOption := s.highlighted(); onOption && choice == initcmd.ConnectAndRemove && !s.checks.done {
		keys = "↑↓ move · removing waits for the server checks"
	}
	return keys
}

func (s *connectScreen) highlighted() (choice initcmd.ConnectChoice, onOption bool) {
	if !s.actions.active || s.actions.onBack() {
		return choice, false
	}
	return s.options()[s.actions.at], true
}

func (s *connectScreen) empty() bool {
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

func (s *connectScreen) chosenConnect() initcmd.ConnectParams {
	return initcmd.ConnectParams{Agents: s.picked(), Choice: s.chosen, Removals: s.checks.removals}
}
