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

const minWidth, minHeight = 60, 12

const (
	headingLines         = 1
	blankLinesAroundBody = 2
	footerLines          = 2
	chromeLines          = headingLines + blankLinesAroundBody + footerLines
)

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
		a.at++
	case back:
		a.at = max(a.at-1, 0)
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
	body := s.body(a.height - chromeLines)
	padding := strings.Repeat("\n", max(a.height-chromeLines-strings.Count(body, "\n")-1, 0))
	return a.fit(bold.Render(s.heading()) + "\n\n" + body + padding + "\n\n" + dim.Render(a.footer(s)))
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

// Two lines, so the footer fits the narrowest window the UI draws in.
func (a *app) footer(s screen) string {
	var leave []string
	if a.at > 0 {
		leave = append(leave, "esc back")
	}
	return s.keys() + "\n" + strings.Join(append(leave, "ctrl+c quit without saving"), " · ")
}
