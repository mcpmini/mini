package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// fakeScreen moves on enter and back on esc, and records the keys it was given.
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
	if a.at != 1 || a.finished {
		t.Fatalf("at = %d, finished = %v; want back on the last screen", a.at, a.finished)
	}
	if cmd := send(a, "enter"); !a.finished || cmd == nil {
		t.Errorf(
			"enter on the last screen: finished = %v, cmd = %v; want the flow finished and the program quit",
			a.finished,
			cmd,
		)
	}
}

func TestApp_ctrlCQuitsFromAnyScreenWithoutPassingTheKeyOn(t *testing.T) {
	first := &fakeScreen{name: "First"}
	a := sized(newApp([]screen{first, &fakeScreen{name: "Last"}}))
	if cmd := send(a, "ctrl+c"); !a.quit || a.finished || cmd == nil || len(first.got) != 0 {
		t.Errorf(
			"quit = %v, finished = %v, cmd = %v, screen saw %v; want quit only",
			a.quit,
			a.finished,
			cmd,
			first.got,
		)
	}
}

func TestApp_footerNamesEnterEscAndCtrlC(t *testing.T) {
	a := sized(newApp([]screen{&fakeScreen{name: "First"}, &fakeScreen{name: "Last"}}))
	first := shown(a)
	send(a, "enter")
	second := shown(a)
	lastLine := func(view string) string { return view[strings.LastIndex(view, "\n")+1:] }
	if strings.Contains(lastLine(first), "esc") || !strings.Contains(lastLine(first), "ctrl+c quit without saving") {
		t.Errorf("first screen's footer = %q, want no esc (it does nothing there) and ctrl+c", lastLine(first))
	}
	if !strings.Contains(lastLine(second), "esc back") {
		t.Errorf("second screen's footer = %q, want esc back", lastLine(second))
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
