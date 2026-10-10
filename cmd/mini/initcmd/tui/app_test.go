package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

type showableScreen interface {
	refresher
	enterer
}

func showScreen(s showableScreen) tea.Cmd {
	s.refresh()
	return s.enter()
}

type fakeScreen struct {
	name    string
	nothing bool
	got     []string
}

func (s *fakeScreen) heading() string { return s.name }
func (s *fakeScreen) body(int) string { return s.name + " body" }
func (s *fakeScreen) keys() string    { return "enter continue" }
func (s *fakeScreen) empty() bool     { return s.nothing }

func (s *fakeScreen) handle(key tea.KeyPressMsg) (step, tea.Cmd) {
	s.got = append(s.got, key.String())
	switch key.String() {
	case "enter":
		return forward, nil
	case "esc":
		return back, nil
	}
	return stay, nil
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
	if a.at != 2 {
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
	send(a, "tab", "enter")
	second := shown(a)
	lastLine := func(view string) string { return view[strings.LastIndex(view, "\n")+1:] }
	if footer := lastLine(first); strings.Contains(footer, "esc") || !strings.Contains(footer, "tab continue") ||
		!strings.Contains(footer, "ctrl+c quit") {
		t.Errorf(
			"first screen's last line = %q, want tab continue and ctrl+c, and no esc (it does nothing there)",
			footer,
		)
	}
	if footer := lastLine(second); !strings.Contains(footer, "enter continue · esc back · ctrl+c") {
		t.Errorf("second screen's last line = %q, want its keys, esc back and ctrl+c together", footer)
	}
	if lines := strings.Count(second, "\n") + 1; lines != 30 {
		t.Errorf("view has %d lines, want the window's 30 so the footer sits at the bottom", lines)
	}
}

type longKeysScreen struct{ fakeScreen }

func (s *longKeysScreen) keys() string {
	return "space tick · a all · / filter · enter continue · ↑↓ move"
}

func TestApp_inTheNarrowestWindowTheKeysSplitInsteadOfBeingCut(t *testing.T) {
	a := newApp([]screen{&longKeysScreen{fakeScreen{name: "Pick"}}})
	a.Update(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	footer := footerOf(shown(a))
	if !strings.Contains(footer, "↑↓ move\nctrl+c quit") || strings.Contains(footer, "…") {
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
		if got := shown(a); !strings.Contains(got, "Window too small") || !strings.Contains(got, "Make it larger") {
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

func TestApp_theAskToEnlargeSitsInTheMiddleOfTheWindow(t *testing.T) {
	a := newApp([]screen{&fakeScreen{name: "First"}})
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	lines := strings.Split(shown(a), "\n")
	row := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, "Window too small") })
	if len(lines) != 10 || row < 2 || row > 4 {
		t.Fatalf("view:\n%s\nwant 10 lines with the message starting near the middle", strings.Join(lines, "\n"))
	}
	for _, text := range []string{"Window too small", "Make it larger"} {
		line := lines[slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, text) })]
		left := len(line) - len(strings.TrimLeft(line, " "))
		if want := (50 - len(text)) / 2; left < want-1 || left > want+1 {
			t.Errorf("%q starts at column %d, want about %d: each line is centered", text, left, want)
		}
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

func (s *enteredScreen) enter() tea.Cmd {
	s.entered++
	return nil
}

func TestApp_aScreenIsToldEachTimeItIsShownAgain(t *testing.T) {
	first, second := &enteredScreen{
		fakeScreen: fakeScreen{name: "First"},
	}, &enteredScreen{
		fakeScreen: fakeScreen{name: "Second"},
	}
	a := sized(newApp([]screen{first, second}))
	send(a, "enter", "esc", "enter")
	if first.entered != 2 || second.entered != 2 {
		t.Errorf(
			"entered: first %d, second %d; want 2 each: the start counts as arriving",
			first.entered,
			second.entered,
		)
	}
}

type refreshedScreen struct {
	fakeScreen
	refreshes int
}

func (s *refreshedScreen) refresh() {
	s.refreshes++
}

func TestApp_drawingReadsNoScreenAgain(t *testing.T) {
	first := &refreshedScreen{fakeScreen: fakeScreen{name: "First"}}
	second := &refreshedScreen{fakeScreen: fakeScreen{name: "Second"}}
	a := sized(newApp([]screen{first, second}))
	send(a, "enter")
	if second.refreshes == 0 {
		t.Fatal("moving onto Second didn't read its rows")
	}
	before := first.refreshes + second.refreshes

	view := shown(a)
	shown(a)

	if after := first.refreshes + second.refreshes; after != before {
		t.Errorf("drawing read the screens %d more times; want none: a frame must not reload files", after-before)
	}
	if footer := footerOf(view); !strings.Contains(footer, "esc back") {
		t.Errorf("footer:\n%s\nwant esc back: First has rows", footer)
	}
}

func TestApp_escOnTheFirstScreenLeavesItAsItWas(t *testing.T) {
	first := &enteredScreen{fakeScreen: fakeScreen{name: "First"}}
	a := sized(newApp([]screen{first}))
	before := first.entered
	send(a, "esc")
	if first.entered != before {
		t.Errorf("esc on the first screen re-entered it %d times; want it untouched", first.entered-before)
	}
}

func TestApp_savesEachTimeTheUserMovesPastTheSavePoint(t *testing.T) {
	saves := 0
	a := sized(newApp([]screen{&fakeScreen{name: "Pick"}, &fakeScreen{name: "After"}}))
	a.saves = savePoint{after: 0, save: func() { saves++ }}
	send(a, "enter")
	if saves != 1 {
		t.Errorf("saves = %d after enter; want 1", saves)
	}
	send(a, "esc", "enter")
	if saves != 2 {
		t.Errorf("saves = %d after going back and forward again; want 2", saves)
	}
}

func TestApp_finishingBeforeTheSavePointSaves(t *testing.T) {
	saves := 0
	a := sized(newApp([]screen{&fakeScreen{name: "Pick"}, &fakeScreen{name: "Empty", nothing: true}}))
	a.saves = savePoint{after: 1, save: func() { saves++ }}
	if cmd := send(a, "enter"); saves != 1 || cmd == nil {
		t.Errorf("saves = %d, cmd = %v; want the picks saved and the program ended", saves, cmd)
	}
}

type wrappingScreen struct {
	fakeScreen
	width int
}

func (s *wrappingScreen) resize(width, _ int) { s.width = width }

func TestApp_aScreenThatWrapsItsLinesLearnsEachWindowWidth(t *testing.T) {
	later := &wrappingScreen{fakeScreen: fakeScreen{name: "Later"}}
	a := newApp([]screen{&fakeScreen{name: "First"}, later})
	for _, width := range []int{100, 60} {
		a.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		if later.width != width {
			t.Errorf("after a resize to %d, the screen not yet shown has width %d", width, later.width)
		}
	}
}

func TestApp_everyScreenButTheFirstEndsWithBack(t *testing.T) {
	first := newImportScreen([]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")})
	second := newImportScreen([]initcmd.Candidate{candidate("notes", "https://notes.example.com/mcp", true, "Codex")})
	a := sized(newApp([]screen{first, second}))
	if view := shown(a); strings.Contains(view, backLabel) {
		t.Errorf("first screen:\n%s\nwant no Back: there is nothing to go back to", view)
	}
	send(a, "tab", "enter")
	if view := shown(a); !strings.Contains(view, "Continue\n  Back") {
		t.Fatalf("second screen:\n%s\nwant Back under Continue", view)
	}
	send(a, "tab", "down", "enter")
	if view := shown(a); !strings.Contains(view, "github") {
		t.Errorf("after enter on Back:\n%s\nwant the first screen again", view)
	}
}

type checkFinished struct{}

func TestApp_backGoesAwayWhenTheScreensBeforeEmptyWhileShown(t *testing.T) {
	logins := &fakeScreen{name: "logins"}
	last := newImportScreen([]initcmd.Candidate{candidate("notes", "https://notes.example.com/mcp", true, "Codex")})
	a := sized(newApp([]screen{logins, last}))
	send(a, "enter")
	if view := shown(a); !strings.Contains(view, "Continue\n  Back") {
		t.Fatalf("second screen:\n%s\nwant Back while the first screen has rows", view)
	}
	logins.nothing = true
	a.Update(checkFinished{})
	if view := shown(a); strings.Contains(view, backLabel) || strings.Contains(view, "esc back") {
		t.Errorf("after the first screen emptied:\n%s\nwant no Back: there is nothing left to go back to", view)
	}
}
