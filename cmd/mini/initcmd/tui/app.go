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
	enter()
}

const minWidth, minHeight = 60, 12

const headingLines, blankLinesAroundBody = 1, 2

type app struct {
	screens []screen
	at      int
	width   int
	height  int
	quit    bool
}

func newApp(screens []screen) *app {
	a := &app{}
	for _, s := range screens {
		if !s.empty() {
			a.screens = append(a.screens, s)
		}
	}
	return a
}

func (a *app) Init() tea.Cmd {
	return nil
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return a, a.handle(msg)
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
		if a.at == len(a.screens)-1 {
			return tea.Quit
		}
		a.show(a.at + 1)
	case back:
		a.show(max(a.at-1, 0))
	}
	return nil
}

func (a *app) show(at int) {
	a.at = at
	if s, ok := a.screens[at].(enterer); ok {
		s.enter()
	}
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
	if a.at > 0 && filter == "" {
		leave = append(leave, "esc back")
	}
	leave = append(leave, "ctrl+c quit without saving")
	if keys := strings.Join(append([]string{s.keys()}, leave...), " · "); ansi.StringWidth(keys) <= a.width {
		return append(lines, dim.Render(keys))
	}
	return append(lines, dim.Render(s.keys()), dim.Render(strings.Join(leave, " · ")))
}
