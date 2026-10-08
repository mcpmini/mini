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

type list struct {
	rows    []row
	checked map[string]bool
	cursor  int
	offset  int
	// header names the columns; it stays above the rows as they scroll. No label means no header.
	header row
}

func newList(rows []row, checked map[string]bool) *list {
	return &list{rows: rows, checked: checked}
}

func (l *list) current() (row, bool) {
	if l.cursor >= len(l.rows) {
		return row{}, false
	}
	return l.rows[l.cursor], true
}

func (l *list) move(step int) {
	l.cursor = min(max(l.cursor+step, 0), max(len(l.rows)-1, 0))
}

func (l *list) toggle() {
	if r, ok := l.current(); ok {
		l.checked[r.key] = !l.checked[r.key]
	}
}

// toggleAll ticks every row, or unticks them all when they are all ticked already.
func (l *list) toggleAll() {
	all := true
	for _, r := range l.rows {
		all = all && l.checked[r.key]
	}
	for _, r := range l.rows {
		l.checked[r.key] = !all
	}
}

func (l *list) handle(key tea.KeyPressMsg) bool {
	switch key.String() {
	case "up":
		l.move(-1)
	case "down":
		l.move(1)
	case "space":
		l.toggle()
	default:
		return false
	}
	return true
}
