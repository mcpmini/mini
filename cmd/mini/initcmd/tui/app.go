package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
	handle(key tea.KeyPressMsg) step
	body(height int) string
	// keys names the screen's own keys and what enter does there; the app adds esc and ctrl+c.
	keys() string
	empty() bool
}

// A screen whose rows depend on earlier screens rebuilds them each time it is shown.
type enterer interface {
	enter() tea.Cmd
}

// A screen that loads in the background starts loading when the UI starts and gets every message.
type loader interface {
	start() tea.Cmd
	update(msg tea.Msg) tea.Cmd
}

// savePoint writes the picks each time the user moves forward past screen after, and before
// finishing if nothing was saved yet. Once saved, quitting keeps what was written.
type savePoint struct {
	after int
	save  func()
	saved bool
}

const minWidth, minHeight = 60, 12

const headingLines, blankLinesAroundBody = 1, 2

type app struct {
	screens []screen
	at      int
	width   int
	height  int
	quit    bool
	saves   savePoint
	// started is what the first screen asked for when it was shown, before the program ran.
	started tea.Cmd
}

// A screen can stop being empty once it loads, so empty screens are skipped when moving, not dropped.
func newApp(screens []screen) *app {
	a := &app{screens: screens, at: -1}
	if first, ok := a.next(-1, 1); ok {
		a.started = a.show(first)
	}
	return a
}

func (a *app) hasScreens() bool {
	return a.at >= 0
}

func (a *app) next(from, direction int) (int, bool) {
	for i := from + direction; i >= 0 && i < len(a.screens); i += direction {
		if !a.screens[i].empty() {
			return i, true
		}
	}
	return 0, false
}

func (a *app) Init() tea.Cmd {
	cmds := []tea.Cmd{a.started}
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
	switch a.screens[a.at].handle(key) {
	case forward:
		return a.forward()
	case back:
		if previous, ok := a.next(a.at, -1); ok {
			return a.show(previous)
		}
	}
	return nil
}

func (a *app) forward() tea.Cmd {
	next, ok := a.next(a.at, 1)
	passes := a.at <= a.saves.after && (!ok || next > a.saves.after)
	// A run that starts past the save point, on Logins, still saves its (empty) picks on finishing.
	if a.saves.save != nil && (passes || (!ok && !a.saves.saved)) {
		a.saves.save()
		a.saves.saved = true
		// What was written decides whether the screens after it have anything to show.
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
		return "Make the window larger"
	}
	s := a.screens[a.at]
	footer := a.footer(s)
	bodyHeight := a.height - headingLines - blankLinesAroundBody - len(footer)
	body := s.body(bodyHeight)
	padding := strings.Repeat("\n", max(bodyHeight-strings.Count(body, "\n")-1, 0))
	return a.fit(bold.Render(s.heading()) + "\n\n" + body + padding + "\n\n" + strings.Join(footer, "\n"))
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
	leave = append(leave, a.quitKey())
	if keys := strings.Join(append([]string{s.keys()}, leave...), " · "); ansi.StringWidth(keys) <= a.width {
		return append(lines, dim.Render(keys))
	}
	return append(lines, dim.Render(s.keys()), dim.Render(strings.Join(leave, " · ")))
}

func (a *app) canGoBack() bool {
	_, ok := a.next(a.at, -1)
	return ok
}

func (a *app) quitKey() string {
	switch {
	case a.saves.saved && a.at <= a.saves.after:
		// Ticks changed since the save aren't written until the user moves past it again.
		return "ctrl+c quit (keeps what was saved before)"
	case a.saves.saved:
		return "ctrl+c quit (servers saved, agents untouched)"
	}
	return "ctrl+c quit without saving"
}
