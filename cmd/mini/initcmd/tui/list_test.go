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

func catalogRows() []row {
	return []row{
		{label: "Popular", heading: true},
		{key: "linear", label: "linear", detail: "issues"},
		{key: "github", label: "github", detail: "code"},
		{label: "Docs", heading: true},
		{key: "notion", label: "notion", detail: "pages", disabled: true},
		{key: "linear", label: "linear", detail: "issues"},
	}
}

func currentKey(l *list) string {
	r, _ := l.current()
	return r.key
}

func TestList_cursorSkipsHeadingsAndGreyedRows(t *testing.T) {
	l := newList(catalogRows(), map[string]bool{})
	if got := currentKey(l); got != "linear" {
		t.Fatalf("cursor starts on %q, want the first row under the first heading", got)
	}
	typeKeys(l, "down", "down")
	if got := currentKey(l); got != "linear" || l.cursor != 5 {
		t.Errorf("cursor = %q at %d, want the second linear row past the Docs heading and greyed notion", got, l.cursor)
	}
	typeKeys(l, "down")
	if l.cursor != 5 {
		t.Errorf("cursor moved past the last row to %d", l.cursor)
	}
}

func TestList_rowsSharingAKeyShareATick(t *testing.T) {
	l := newList(catalogRows(), map[string]bool{})
	typeKeys(l, "space")
	if got := strings.Count(plainView(l, 20), "[x] linear"); got != 2 {
		t.Errorf("ticked rows for linear = %d, want both rows:\n%s", got, plainView(l, 20))
	}
}

func TestList_toggleAllTicksEverySelectableRowThenNone(t *testing.T) {
	checked := map[string]bool{"github": true}
	l := newList(catalogRows(), checked)
	l.toggleAll()
	if !checked["linear"] || !checked["github"] || checked["notion"] {
		t.Fatalf("after one toggle-all: %v, want every selectable row ticked and greyed notion not", checked)
	}
	l.toggleAll()
	if checked["linear"] || checked["github"] {
		t.Errorf("after a second toggle-all: %v, want none ticked", checked)
	}
}

func TestList_filter(t *testing.T) {
	t.Run("lists each match once under its heading", func(t *testing.T) {
		l := newList(catalogRows(), map[string]bool{})
		typeKeys(l, "/", "l", "i", "n")
		view := plainView(l, 20)
		if strings.Count(view, "linear") != 1 || !strings.Contains(view, "Popular") || strings.Contains(view, "Docs") ||
			strings.Contains(view, "github") {
			t.Errorf("filtered view:\n%s\nwant linear once, under Popular, and nothing else", view)
		}
	})
	t.Run("typed characters edit the filter instead of acting", func(t *testing.T) {
		checked := map[string]bool{}
		l := newList(catalogRows(), checked)
		typeKeys(l, "/", "space")
		if l.filter != " " || checked["linear"] {
			t.Errorf(
				"filter = %q, checked = %v; want a space typed into the filter and nothing ticked",
				l.filter,
				checked,
			)
		}
	})
	t.Run("enter keeps the filter and leaves filter mode", func(t *testing.T) {
		l := newList(catalogRows(), map[string]bool{})
		typeKeys(l, "/", "g", "enter")
		if l.filtering || l.filter != "g" || currentKey(l) != "github" {
			t.Errorf(
				"filtering = %v, filter = %q, cursor on %q; want filter g kept on github",
				l.filtering,
				l.filter,
				currentKey(l),
			)
		}
	})
	t.Run("esc clears the filter", func(t *testing.T) {
		l := newList(catalogRows(), map[string]bool{})
		typeKeys(l, "/", "g", "esc")
		if l.filtering || l.filter != "" || len(l.visible()) != len(catalogRows()) {
			t.Errorf("filtering = %v, filter = %q; want every row back", l.filtering, l.filter)
		}
	})
	t.Run("backspace removes the last character", func(t *testing.T) {
		l := newList(catalogRows(), map[string]bool{})
		typeKeys(l, "/", "g", "x", "backspace")
		if l.filter != "g" {
			t.Errorf("filter = %q, want g", l.filter)
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
