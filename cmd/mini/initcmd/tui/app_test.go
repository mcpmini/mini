package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

type fakeScreen struct {
	name    string
	nothing bool
	got     []string
}

func (s *fakeScreen) heading() string { return s.name }
func (s *fakeScreen) body(int) string { return s.name + " body" }
func (s *fakeScreen) keys() string    { return "enter continue" }
func (s *fakeScreen) empty() bool     { return s.nothing }

func (s *fakeScreen) handle(key tea.KeyPressMsg) step {
	s.got = append(s.got, key.String())
	switch key.String() {
	case "enter":
		return forward
	case "esc":
		return back
	}
	return stay
}

func sized(a *app) *app {
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return a
}

func shown(a *app) string {
	return ansi.Strip(a.render())
}

func send(a *app, names ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, name := range names {
		_, cmd = a.Update(press(name))
	}
	return cmd
}

func TestApp_movesThroughTheScreensItHasSomethingFor(t *testing.T) {
	first := &fakeScreen{name: "First"}
	skipped := &fakeScreen{name: "Skipped", nothing: true}
	last := &fakeScreen{name: "Last"}
	a := sized(newApp([]screen{first, skipped, last}))

	send(a, "esc")
	if a.at != 0 {
		t.Fatalf("esc on the first screen moved to screen %d", a.at)
	}
	send(a, "enter")
	if view := shown(a); !strings.HasPrefix(view, "Last") {
		t.Errorf("after enter:\n%s\nwant the Last screen; the empty one is skipped", view)
	}
	send(a, "esc", "enter")
	if a.at != 1 {
		t.Fatalf("at = %d; want back on the last screen", a.at)
	}
	if cmd := send(a, "enter"); a.quit || cmd == nil {
		t.Errorf("enter on the last screen: quit = %v, cmd = %v; want the program ended without quitting", a.quit, cmd)
	}
}

func TestApp_ctrlCQuitsFromAnyScreenWithoutPassingTheKeyOn(t *testing.T) {
	first := &fakeScreen{name: "First"}
	a := sized(newApp([]screen{first, &fakeScreen{name: "Last"}}))
	if cmd := send(a, "ctrl+c"); !a.quit || cmd == nil || len(first.got) != 0 {
		t.Errorf("quit = %v, cmd = %v, screen saw %v; want quit only", a.quit, cmd, first.got)
	}
}

func footerOf(view string) string {
	lines := strings.Split(view, "\n")
	return strings.Join(lines[len(lines)-2:], "\n")
}

func TestApp_footerNamesEnterEscAndCtrlCWithinTheNarrowestWindow(t *testing.T) {
	imports := newImportScreen([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	a := sized(newApp([]screen{imports, &fakeScreen{name: "Last"}}))
	first := footerOf(shown(a))
	send(a, "enter")
	second := shown(a)
	if strings.Contains(first, "esc") || !strings.Contains(first, "enter continue") ||
		!strings.Contains(first, "ctrl+c quit without saving") {
		t.Errorf("first screen's footer = %q, want enter and ctrl+c, and no esc (it does nothing there)", first)
	}
	if !strings.Contains(footerOf(second), "esc back") {
		t.Errorf("second screen's footer = %q, want esc back", footerOf(second))
	}
	for _, line := range strings.Split(first, "\n") {
		if len([]rune(line)) > minWidth {
			t.Errorf("footer line %q is wider than the %d columns the UI draws in", line, minWidth)
		}
	}
	if lines := strings.Count(second, "\n") + 1; lines != 30 {
		t.Errorf("view has %d lines, want the window's 30 so the footer sits at the bottom", lines)
	}
}

func TestApp_aSmallWindowShowsOnlyAskToEnlarge(t *testing.T) {
	a := newApp([]screen{&fakeScreen{name: "First"}})
	for _, size := range []tea.WindowSizeMsg{{Width: 59, Height: 30}, {Width: 100, Height: 11}} {
		a.Update(size)
		if got := shown(a); got != "Make the window larger" {
			t.Errorf("%dx%d renders %q", size.Width, size.Height, got)
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	if got := shown(a); !strings.HasPrefix(got, "First") {
		t.Errorf("60x12 renders %q, want the screen", got)
	}
}

func TestApp_aLineWiderThanTheWindowIsCutSoTheFooterStaysPut(t *testing.T) {
	a := newApp([]screen{&fakeScreen{name: strings.Repeat("wide ", 30)}})
	a.Update(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	view := shown(a)
	lines := strings.Split(view, "\n")
	if len(lines) != minHeight || ansi.StringWidth(lines[0]) != minWidth || !strings.HasSuffix(lines[0], "…") {
		t.Errorf(
			"view (%d lines):\n%s\nwant %d lines, the heading cut to %d columns",
			len(lines),
			view,
			minHeight,
			minWidth,
		)
	}
}
