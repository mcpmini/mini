package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func press(name string) tea.KeyPressMsg {
	switch name {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(name)[0]
	return tea.KeyPressMsg{Code: r, Text: name}
}

func plainView(l *list, height int) string {
	return ansi.Strip(l.view(height))
}

func typeKeys(l *list, names ...string) {
	for _, name := range names {
		l.handle(press(name))
	}
}

func serverRows() []row {
	return []row{
		{key: "linear", label: "linear", detail: "issues"},
		{key: "github", label: "github", detail: "code"},
		{key: "notion", label: "notion", detail: "pages"},
	}
}

func currentKey(l *list) string {
	r, _ := l.current()
	return r.key
}

func TestList_cursorStaysWithinTheRows(t *testing.T) {
	l := newList(serverRows(), map[string]bool{})
	typeKeys(l, "up")
	if got := currentKey(l); got != "linear" {
		t.Errorf("after up on the first row: cursor on %q, want linear", got)
	}
	typeKeys(l, "down", "down", "down")
	if got := currentKey(l); got != "notion" {
		t.Errorf("after moving past the last row: cursor on %q, want notion", got)
	}
}

func TestList_spaceTicksTheRowUnderTheCursor(t *testing.T) {
	checked := map[string]bool{}
	l := newList(serverRows(), checked)
	typeKeys(l, "down", "space")
	if view := plainView(l, 20); !checked["github"] || !strings.Contains(view, "> [x] github") || checked["linear"] {
		t.Errorf("checked = %v, view:\n%s\nwant github ticked under the cursor", checked, view)
	}
}

func TestList_toggleAllTicksEveryRowThenNone(t *testing.T) {
	checked := map[string]bool{"github": true}
	l := newList(serverRows(), checked)
	l.toggleAll()
	if !checked["linear"] || !checked["github"] || !checked["notion"] {
		t.Fatalf("after one toggle-all: %v, want every row ticked", checked)
	}
	l.toggleAll()
	if checked["linear"] || checked["github"] || checked["notion"] {
		t.Errorf("after a second toggle-all: %v, want none ticked", checked)
	}
}

func TestList_toggleAllLeavesRowsTheFilterHides(t *testing.T) {
	checked := map[string]bool{}
	l := newList(serverRows(), checked)
	typeKeys(l, "/", "g", "i", "t", "enter")
	l.toggleAll()
	if !checked["github"] || checked["linear"] || checked["notion"] {
		t.Errorf("checked = %v, want only github, the one row the filter shows", checked)
	}
}

func TestList_filter(t *testing.T) {
	t.Run("shows only matching rows, by name or detail", func(t *testing.T) {
		l := newList(serverRows(), map[string]bool{})
		typeKeys(l, "/", "p", "a", "g")
		if view := plainView(l, 20); !strings.Contains(view, "notion") || strings.Contains(view, "linear") ||
			strings.Contains(view, "github") {
			t.Errorf("filtered view:\n%s\nwant only notion, whose detail matches", view)
		}
	})
	t.Run("typed characters edit the filter instead of acting", func(t *testing.T) {
		checked := map[string]bool{}
		l := newList(serverRows(), checked)
		typeKeys(l, "/", "space")
		if l.filter.text != " " || checked["linear"] {
			t.Errorf(
				"filter = %q, checked = %v; want a space typed into the filter and nothing ticked",
				l.filter.text,
				checked,
			)
		}
	})
	t.Run("enter keeps the filter and leaves filter mode", func(t *testing.T) {
		l := newList(serverRows(), map[string]bool{})
		typeKeys(l, "/", "g", "i", "t", "enter", "space")
		if l.filter.typing || l.filter.text != "git" || !l.checked["github"] {
			t.Errorf(
				"filtering = %v, filter = %q, checked = %v; want filter git kept and github ticked",
				l.filter.typing,
				l.filter.text,
				l.checked,
			)
		}
	})
	t.Run("esc clears the filter", func(t *testing.T) {
		l := newList(serverRows(), map[string]bool{})
		typeKeys(l, "/", "g", "esc")
		if l.filter.typing || l.filter.text != "" || len(l.visible()) != len(serverRows()) {
			t.Errorf("filtering = %v, filter = %q; want every row back", l.filter.typing, l.filter.text)
		}
	})
	t.Run("esc after enter clears the applied filter", func(t *testing.T) {
		l := newList(serverRows(), map[string]bool{})
		typeKeys(l, "/", "g", "enter")
		if !l.handle(press("esc")) || l.filter.text != "" {
			t.Errorf("filter = %q; want esc taken by the list and the filter cleared", l.filter.text)
		}
		if l.handle(press("esc")) {
			t.Error("esc with no filter was taken by the list; want it left for the screen")
		}
	})
	t.Run("backspace removes the last character", func(t *testing.T) {
		l := newList(serverRows(), map[string]bool{})
		typeKeys(l, "/", "g", "é", "backspace")
		if l.filter.text != "g" {
			t.Errorf("filter = %q, want g", l.filter.text)
		}
	})
}

func TestList_scrollsToKeepTheCursorShown(t *testing.T) {
	var rows []row
	for i := range 30 {
		name := fmt.Sprintf("server-%02d", i)
		rows = append(rows, row{key: name, label: name})
	}
	l := newList(rows, map[string]bool{})
	for range 12 {
		l.handle(press("down"))
	}
	view := plainView(l, 5)
	if lines := strings.Count(view, "\n") + 1; lines != 5 || !strings.Contains(view, "> [ ] server-12") {
		t.Errorf("view (%d lines):\n%s\nwant 5 lines with the cursor on server-12", lines, view)
	}
	for range 12 {
		l.handle(press("up"))
	}
	if view := plainView(l, 5); !strings.HasPrefix(view, "> [ ] server-00") {
		t.Errorf("view after moving back up:\n%s\nwant it scrolled back to server-00", view)
	}
}

func TestList_scrollingKeepsTheCursorRowsSubtitleShown(t *testing.T) {
	var rows []row
	for i := range 10 {
		name := fmt.Sprintf("server-%02d", i)
		rows = append(rows, row{key: name, label: name, subtitle: "why " + name})
	}
	l := newList(rows, map[string]bool{})
	for range 4 {
		l.handle(press("down"))
	}
	view := plainView(l, 7)
	if lines := strings.Split(view, "\n"); len(lines) != 7 ||
		!strings.HasSuffix(view, "> [ ] server-04\n      why server-04\n\n  Continue") {
		t.Errorf("view (%d lines):\n%s\nwant 7 lines: server-04 and its subtitle above Continue", len(lines), view)
	}
}
