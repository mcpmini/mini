package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type step int

const (
	stay step = iota
	forward
	back
)

type screen interface {
	heading() string
	handle(key tea.KeyPressMsg) (step, tea.Cmd)
	body(height int) string
	// keys names the screen's own keys and what enter does there; the app adds esc and ctrl+c.
	keys() string
	// empty answers from what the screen last read, so drawing a frame never reads files.
	empty() bool
}

// A screen whose rows depend on what earlier screens wrote reads them again each time the app
// moves past or onto it.
type refresher interface {
	refresh()
}

// A screen that starts over each time it is shown.
type enterer interface {
	enter() tea.Cmd
}

// A screen that loads in the background starts loading when the UI starts and gets every message.
type loader interface {
	start() tea.Cmd
	update(msg tea.Msg) tea.Cmd
}

type resizer interface {
	resize(width int)
}

func (a *app) resizeScreens() {
	for _, s := range a.screens {
		if r, ok := s.(resizer); ok {
			r.resize(a.width)
		}
	}
}

// savePoint saves the picks each time the user moves forward past screen after.
type savePoint struct {
	after int
	save  func()
	saved bool
}

const minWidth, minHeight = 60, 12

const headingLines, blankLinesAroundBody = 1, 2

type app struct {
	screens        []screen
	at             int
	width          int
	height         int
	quit           bool
	saves          savePoint
	firstScreenCmd tea.Cmd
}

// A screen can stop being empty once it loads, so empty screens are skipped when moving, not dropped.
func newApp(screens []screen) *app {
	a := &app{screens: screens, at: -1}
	if first, ok := a.next(-1, 1); ok {
		a.firstScreenCmd = a.show(first)
	}
	return a
}

func (a *app) hasScreens() bool {
	return a.at >= 0
}

func (a *app) next(from, direction int) (int, bool) {
	for i := from + direction; i >= 0 && i < len(a.screens); i += direction {
		if r, ok := a.screens[i].(refresher); ok {
			r.refresh()
		}
		if !a.screens[i].empty() {
			return i, true
		}
	}
	return 0, false
}

func (a *app) Init() tea.Cmd {
	cmds := []tea.Cmd{a.firstScreenCmd}
	for _, s := range a.screens {
		if l, ok := s.(loader); ok {
			cmds = append(cmds, l.start())
		}
	}
	return tea.Batch(cmds...)
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resizeScreens()
	case tea.KeyPressMsg:
		return a, a.handle(msg)
	default:
		var cmds []tea.Cmd
		for _, s := range a.screens {
			if l, ok := s.(loader); ok {
				cmds = append(cmds, l.update(msg))
			}
		}
		return a, tea.Batch(cmds...)
	}
	return a, nil
}

func (a *app) handle(key tea.KeyPressMsg) tea.Cmd {
	if key.String() == "ctrl+c" {
		a.quit = true
		return tea.Quit
	}
	if a.tooSmall() {
		// The screen is hidden, so a key would act on picks the user can't see.
		return nil
	}
	move, work := a.screens[a.at].handle(key)
	switch move {
	case forward:
		return tea.Batch(work, a.forward())
	case back:
		if previous, ok := a.next(a.at, -1); ok {
			return tea.Batch(work, a.show(previous))
		}
	}
	return work
}

func (a *app) forward() tea.Cmd {
	next, ok := a.next(a.at, 1)
	passes := a.at <= a.saves.after && (!ok || next > a.saves.after)
	// A run that starts on Logins, past the save point, still saves before finishing.
	if a.saves.save != nil && (passes || (!ok && !a.saves.saved)) {
		a.saves.save()
		a.saves.saved = true
		// Saving can empty or fill the screens after it.
		next, ok = a.next(a.at, 1)
	}
	if !ok {
		return tea.Quit
	}
	return a.show(next)
}

func (a *app) show(at int) tea.Cmd {
	a.at = at
	if s, ok := a.screens[at].(enterer); ok {
		return s.enter()
	}
	return nil
}

func (a *app) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	return v
}

func (a *app) render() string {
	if a.tooSmall() {
		return a.askToEnlarge()
	}
	s := a.screens[a.at]
	footer := a.footer(s)
	bodyHeight := a.height - headingLines - blankLinesAroundBody - len(footer)
	body := s.body(bodyHeight)
	padding := strings.Repeat("\n", max(bodyHeight-strings.Count(body, "\n")-1, 0))
	return a.fit(bold.Render(s.heading()) + "\n\n" + body + padding + "\n\n" + strings.Join(footer, "\n"))
}

func (a *app) askToEnlarge() string {
	message := lipgloss.JoinVertical(
		lipgloss.Center,
		bold.Render("Window too small"),
		"Make it larger",
		"",
		dim.Render("even mini can't make"),
		dim.Render("things this small"),
	)
	return a.fit(lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, message))
}

func (a *app) tooSmall() bool {
	return a.width < minWidth || a.height < minHeight
}

// A line wider than the window would wrap and push the footer off the screen.
func (a *app) fit(view string) string {
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, a.width, "…")
	}
	return strings.Join(lines, "\n")
}

func filterLine(s screen) string {
	if f, ok := s.(interface{ filterLine() string }); ok {
		return f.filterLine()
	}
	return ""
}

// Like less and vim, a filter sits just above the keys; the keys split over two lines only when
// one would be cut at the window's edge.
func (a *app) footer(s screen) []string {
	var lines []string
	filter := filterLine(s)
	if filter != "" {
		lines = append(lines, filter)
	}
	var leave []string
	// esc clears an active filter before it goes back.
	if a.canGoBack() && filter == "" {
		leave = append(leave, "esc back")
	}
	leave = append(leave, "ctrl+c quit")
	if keys := strings.Join(append([]string{s.keys()}, leave...), " · "); ansi.StringWidth(keys) <= a.width {
		return append(lines, dim.Render(keys))
	}
	return append(lines, dim.Render(s.keys()), dim.Render(strings.Join(leave, " · ")))
}

func (a *app) canGoBack() bool {
	// next would refresh screens, which a frame must not do; empty answers from the last read.
	return slices.ContainsFunc(a.screens[:a.at], func(s screen) bool { return !s.empty() })
}
