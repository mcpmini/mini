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

func TestApp_footerNamesEnterEscAndCtrlCOnOneLine(t *testing.T) {
	imports := newImportScreen([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	a := sized(newApp([]screen{imports, &fakeScreen{name: "Last"}}))
	first := shown(a)
	send(a, "enter")
	second := shown(a)
	lastLine := func(view string) string { return view[strings.LastIndex(view, "\n")+1:] }
	if footer := lastLine(first); strings.Contains(footer, "esc") || !strings.Contains(footer, "enter continue") ||
		!strings.Contains(footer, "ctrl+c quit without saving") {
		t.Errorf("first screen's last line = %q, want enter and ctrl+c, and no esc (it does nothing there)", footer)
	}
	if footer := lastLine(second); !strings.Contains(footer, "enter continue · esc back · ctrl+c") {
		t.Errorf("second screen's last line = %q, want its keys, esc back and ctrl+c together", footer)
	}
	if lines := strings.Count(second, "\n") + 1; lines != 30 {
		t.Errorf("view has %d lines, want the window's 30 so the footer sits at the bottom", lines)
	}
}

func TestApp_inTheNarrowestWindowTheKeysSplitInsteadOfBeingCut(t *testing.T) {
	imports := newImportScreen([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	a := newApp([]screen{imports})
	a.Update(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	footer := footerOf(shown(a))
	if !strings.Contains(footer, "enter continue\nctrl+c quit without saving") || strings.Contains(footer, "…") {
		t.Errorf("footer:\n%s\nwant the keys on one line and ctrl+c on the next, nothing cut", footer)
	}
}

func TestApp_aFilterSitsJustAboveTheKeysAndEscClearsItInsteadOfGoingBack(t *testing.T) {
	imports := newImportScreen([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	a := sized(newApp([]screen{&fakeScreen{name: "First"}, imports}))
	send(a, "enter", "/", "g")
	if footer := footerOf(
		shown(a),
	); !strings.HasPrefix(footer, "/g_\ntype to filter") ||
		strings.Contains(footer, "esc back") {
		t.Errorf("footer while typing:\n%s\nwant the filter, then its keys, and no esc back", footer)
	}
	send(a, "enter")
	if footer := footerOf(
		shown(a),
	); !strings.HasPrefix(footer, "/g\n") ||
		!strings.Contains(footer, "esc clear filter") ||
		strings.Contains(footer, "esc back") {
		t.Errorf("footer with the filter kept:\n%s\nwant the filter above keys that say esc clears it", footer)
	}
	send(a, "esc", "esc")
	if a.at != 0 {
		t.Errorf("at = %d after esc twice; want the first esc to clear the filter and the second to go back", a.at)
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
	first := a.screens[0].(*fakeScreen)
	if cmd := send(a, "space", "enter"); cmd != nil || len(first.got) != 0 {
		t.Errorf("keys behind the small-window message reached the screen: %v", first.got)
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

type enteredScreen struct {
	fakeScreen
	entered int
}

func (s *enteredScreen) enter() { s.entered++ }

func TestApp_aScreenIsToldEachTimeItIsShownAgain(t *testing.T) {
	first, second := &enteredScreen{
		fakeScreen: fakeScreen{name: "First"},
	}, &enteredScreen{
		fakeScreen: fakeScreen{name: "Second"},
	}
	a := sized(newApp([]screen{first, second}))
	send(a, "enter", "esc", "enter")
	if first.entered != 1 || second.entered != 2 {
		t.Errorf("entered: first %d, second %d; want 1 and 2, once per arrival", first.entered, second.entered)
	}
}
