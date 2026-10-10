package tui

import (
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
	return matchesFilter(filter, r.label, r.detail)
}

// The cursor indexes the rows the filter shows.
type list struct {
	rows    []row
	checked map[string]bool
	cursor  int
	actions actions
	scroll  scroll
	filter  textFilter
	// header names the columns; it stays above the rows as they scroll. No label means no header.
	header row
}

func newList(rows []row, checked map[string]bool) *list {
	return &list{rows: rows, checked: checked, actions: newActions()}
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

// Moving down past the last row reaches the actions.
func (l *list) move(step int) {
	switch {
	case l.actions.active:
		l.actions.move(step)
	case step > 0 && l.cursor >= len(l.visible())-1:
		l.actions.reach()
	default:
		l.cursor = min(max(l.cursor+step, 0), max(len(l.visible())-1, 0))
	}
}

func (l *list) toggle() {
	if r, ok := l.current(); ok && !l.actions.active {
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
	l.cursor, l.actions.active, l.scroll = 0, false, scroll{}
}

func (l *list) keys(screenKeys string) string {
	return l.filter.keys(screenKeys)
}

// handle reports whether the list took the key; enter on an action is left to the screen.
func (l *list) handle(key tea.KeyPressMsg) bool {
	if l.filter.handle(key, l) {
		return true
	}
	switch key.String() {
	case "up", "down":
		l.moveKey(key.String())
	case "space":
		l.toggle()
	case "enter":
		if l.actions.active {
			return false
		}
		l.toggle()
	case "tab":
		l.actions.toggle()
	default:
		return false
	}
	return true
}

func (l *list) moveKey(key string) {
	if key == "up" {
		l.move(-1)
		return
	}
	l.move(1)
}
