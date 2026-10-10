package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type row struct {
	key    string
	label  string
	detail string
	// subtitle is a dim line under the row; it never scrolls out of view with the cursor on it.
	subtitle string
}

func (r row) matches(filter string) bool {
	filter = strings.ToLower(filter)
	return slices.ContainsFunc([]string{r.label, r.detail}, func(text string) bool {
		return strings.Contains(strings.ToLower(text), filter)
	})
}

// The cursor indexes the rows the filter shows.
type list struct {
	rows    []row
	checked map[string]bool
	cursor  int
	// onContinue puts the cursor on the Continue row under the rows; enter there is the screen's.
	onContinue bool
	scroll     scroll
	filter     textFilter
	// header names the columns; it stays above the rows as they scroll. No label means no header.
	header row
}

func newList(rows []row, checked map[string]bool) *list {
	return &list{rows: rows, checked: checked}
}

func (l *list) visible() []row {
	if l.filter.text == "" {
		return l.rows
	}
	var shown []row
	for _, r := range l.rows {
		if r.matches(l.filter.text) {
			shown = append(shown, r)
		}
	}
	return shown
}

func (l *list) current() (row, bool) {
	shown := l.visible()
	if l.cursor >= len(shown) {
		return row{}, false
	}
	return shown[l.cursor], true
}

// Moving down past the last row reaches Continue, and up from Continue returns to the rows.
func (l *list) move(step int) {
	switch {
	case l.onContinue:
		l.onContinue = step > 0
	case step > 0 && l.cursor >= len(l.visible())-1:
		l.onContinue = true
	default:
		l.cursor = min(max(l.cursor+step, 0), max(len(l.visible())-1, 0))
	}
}

func (l *list) toggle() {
	if l.onContinue {
		return
	}
	if r, ok := l.current(); ok {
		l.checked[r.key] = !l.checked[r.key]
	}
}

// Rows the filter hides keep their ticks: the user can't see them change.
func (l *list) toggleAll() {
	shown := l.visible()
	all := true
	for _, r := range shown {
		all = all && l.checked[r.key]
	}
	for _, r := range shown {
		l.checked[r.key] = !all
	}
}

func (l *list) filterChanged() {
	l.cursor, l.onContinue, l.scroll = 0, false, scroll{}
}

func (l *list) keys(screenKeys string) string {
	return l.filter.keys(screenKeys)
}

func (l *list) handle(key tea.KeyPressMsg) bool {
	if l.filter.typing {
		changed, move := l.filter.typingKey(key)
		if changed {
			l.filterChanged()
		}
		if move {
			l.moveBy(key)
		}
		return true
	}
	return l.moveBy(key) || l.act(key.String())
}

// act reports whether the list took the key; enter on Continue is left to the screen.
func (l *list) act(key string) bool {
	switch key {
	case "space":
		l.toggle()
	case "enter":
		if l.onContinue {
			return false
		}
		l.toggle()
	case "tab":
		l.onContinue = !l.onContinue
	case "/":
		l.filter.typing, l.onContinue = true, false
	case "esc":
		if !l.filter.set("") {
			return false
		}
		l.filterChanged()
	default:
		return false
	}
	return true
}

func (l *list) moveBy(key tea.KeyPressMsg) bool {
	switch key.String() {
	case "up":
		l.move(-1)
	case "down":
		l.move(1)
	default:
		return false
	}
	return true
}
