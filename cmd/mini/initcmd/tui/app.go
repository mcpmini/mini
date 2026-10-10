package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type reply int

const (
	handled reply = iota
	unhandled
	pastLastRow
)

// direction is the step an arrow key takes along a list or across the grid.
func direction(key string) int {
	if key == "up" || key == "left" {
		return -1
	}
	return 1
}

// A screen is one step of the wizard and draws only its own rows; the app draws Continue and Back
// under every screen and moves between them.
type screen interface {
	heading() string
	// handle gets keys only while the cursor is on the screen's rows; tab and esc it leaves
	// unhandled go to the app.
	handle(key tea.KeyPressMsg) (reply, tea.Cmd)
	body(height int, focused bool) string
	keys() string
	focusable() bool
	// empty answers from what the screen last read, so drawing a frame never reads files.
	empty() bool
}

// A screen whose rows depend on what earlier screens wrote reads them again each time the app
// moves past or onto it.
type refresher interface {
	refresh()
}

type enterer interface {
	enter() tea.Cmd
}

type leaver interface {
	leave()
}

// A screen that takes esc for itself, as Logins does to cancel a login, names it in its own keys.
type escTaker interface {
	takesEsc() bool
}

// On the screen's rows esc clears an active filter, or does what the screen takes it for, before it goes back.
func (a *app) screenTakesEsc() bool {
	if a.onNavigation() {
		return false
	}
	t, ok := a.current().(escTaker)
	return filterLine(a.current()) != "" || ok && t.takesEsc()
}

type waiter interface {
	waiting() bool
}

// A screen that loads in the background starts loading when the UI starts.
type loader interface {
	start() tea.Cmd
}

// A screen that waits on background work gets every background message, whether it is shown or not.
type listener interface {
	update(msg tea.Msg) tea.Cmd
}

// A screen that lays its rows out learns the width and the height they get.
type resizer interface {
	resize(width, rowsHeight int)
}

// The app's footer at its tallest: a filter line, and keys wrapped onto two lines.
const tallestFooter = 3

// rowsHeight is what a screen's rows get with the footer and navigation at their tallest: the
// footer grows a line when its keys wrap and Back comes and goes, and a layout picked from the
// height left over would switch with them.
func (a *app) rowsHeight() int {
	return a.height - headingLines - blankLinesAroundBody - tallestFooter - tallestNavigation
}

func (a *app) resizeScreens() {
	for _, s := range a.screens {
		if r, ok := s.(resizer); ok {
			r.resize(a.width, a.rowsHeight())
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
	nav            navigation
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
	cmd := a.update(msg)
	if a.hasScreens() {
		a.clampNavigationCursor()
	}
	return a, cmd
}

func (a *app) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resizeScreens()
	case tea.KeyPressMsg:
		return a.handle(msg)
	default:
		var cmds []tea.Cmd
		for _, s := range a.screens {
			if l, ok := s.(listener); ok {
				cmds = append(cmds, l.update(msg))
			}
		}
		return tea.Batch(cmds...)
	}
	return nil
}

func (a *app) current() screen {
	return a.screens[a.at]
}

func (a *app) handle(key tea.KeyPressMsg) tea.Cmd {
	if key.String() == "ctrl+c" {
		a.quit = true
		return tea.Quit
	}
	switch {
	case a.tooSmall():
		// The screen is hidden, so a key would act on picks the user can't see.
		return nil
	case a.waiting():
		if key.String() == "esc" {
			return a.back()
		}
		return nil
	case a.onNavigation():
		return a.handleNavigation(key)
	}
	return a.handleScreen(key)
}

func (a *app) handleScreen(key tea.KeyPressMsg) tea.Cmd {
	r, work := a.current().handle(key)
	switch {
	case r == pastLastRow, r == unhandled && key.String() == "tab":
		a.reachNavigation()
	case r == unhandled && key.String() == "esc":
		return tea.Batch(work, a.back())
	}
	return work
}

func (a *app) waiting() bool {
	w, ok := a.current().(waiter)
	return ok && w.waiting()
}

func (a *app) back() tea.Cmd {
	previous, ok := a.next(a.at, -1)
	if !ok {
		return nil
	}
	a.leave()
	return a.show(previous)
}

func (a *app) leave() {
	if l, ok := a.current().(leaver); ok {
		l.leave()
	}
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
	a.leave()
	if !ok {
		return tea.Quit
	}
	return a.show(next)
}

func (a *app) show(at int) tea.Cmd {
	a.moveTo(at)
	if s, ok := a.screens[at].(enterer); ok {
		return s.enter()
	}
	return nil
}

func (a *app) moveTo(at int) {
	a.at = at
	a.nav = navigation{}
	// Picking a choice is what a choosing screen is for.
	if _, choosing := a.current().(chooser); choosing {
		a.nav.focus = focusNavigation
	}
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
	s := a.current()
	footer := a.footer(s)
	bodyHeight := a.height - headingLines - blankLinesAroundBody - len(footer)
	body := a.body(bodyHeight)
	padding := strings.Repeat("\n", max(bodyHeight-strings.Count(body, "\n")-1, 0))
	return a.fit(bold.Render(s.heading()) + "\n\n" + body + padding + "\n\n" + strings.Join(footer, "\n"))
}

func (a *app) body(height int) string {
	if a.waiting() {
		return a.current().body(height, false)
	}
	nav, first, last := a.navigationLines()
	var lines []string
	if body := a.current().body(max(height-len(nav), 1), !a.onNavigation()); body != "" {
		lines = strings.Split(body, "\n")
	} else {
		// With nothing above them, the leading blank line would only push the rows down.
		nav, first, last = nav[1:], first-1, last-1
	}
	if first < 0 {
		first, last = 0, 0
	} else {
		first, last = len(lines)+first, len(lines)+last
	}
	return strings.Join(a.nav.scroll.cut(append(lines, nav...), first, last, height), "\n")
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
	if a.canGoBack() && !a.screenTakesEsc() {
		leave = append(leave, "esc back")
	}
	leave = append(leave, "ctrl+c quit")
	keys := a.keys(s)
	if all := strings.Join(append([]string{keys}, leave...), " · "); ansi.StringWidth(all) <= a.width {
		return append(lines, dim.Render(all))
	}
	return append(lines, dim.Render(keys), dim.Render(strings.Join(leave, " · ")))
}

func (a *app) keys(s screen) string {
	if a.onNavigation() && !a.waiting() {
		return a.navigationKeys()
	}
	return s.keys()
}

func (a *app) canGoBack() bool {
	// next refreshes screens, which a frame must not do.
	return slices.ContainsFunc(a.screens[:a.at], func(s screen) bool { return !s.empty() })
}
