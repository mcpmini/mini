package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type row struct {
	key    string
	label  string
	detail string
}

func (r row) matches(filter string) bool {
	filter = strings.ToLower(filter)
	return strings.Contains(strings.ToLower(r.label), filter) || strings.Contains(strings.ToLower(r.detail), filter)
}

// list is a checkbox list. The cursor indexes the rows the filter shows.
type list struct {
	rows      []row
	checked   map[string]bool
	cursor    int
	offset    int
	filter    string
	filtering bool
}

func newList(rows []row, checked map[string]bool) *list {
	return &list{rows: rows, checked: checked}
}

func (l *list) visible() []row {
	if l.filter == "" {
		return l.rows
	}
	var shown []row
	for _, r := range l.rows {
		if r.matches(l.filter) {
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

func (l *list) move(step int) {
	l.cursor = min(max(l.cursor+step, 0), max(len(l.visible())-1, 0))
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

func (l *list) setFilter(filter string) {
	l.filter = filter
	l.cursor, l.offset = 0, 0
}

// In filter mode the list takes every key, so typed characters never act as commands.
func (l *list) handle(key tea.KeyPressMsg) bool {
	if l.filtering {
		l.handleFilterKey(key)
		return true
	}
	switch key.String() {
	case "up":
		l.move(-1)
	case "down":
		l.move(1)
	case "space":
		l.toggle()
	case "/":
		l.filtering = true
	default:
		return false
	}
	return true
}

func (l *list) handleFilterKey(key tea.KeyPressMsg) {
	switch key.String() {
	case "enter":
		l.filtering = false
	case "esc":
		l.filtering = false
		l.setFilter("")
	case "backspace":
		if l.filter != "" {
			l.setFilter(l.filter[:len(l.filter)-1])
		}
	case "up":
		l.move(-1)
	case "down":
		l.move(1)
	default:
		if key.Text != "" {
			l.setFilter(l.filter + key.Text)
		}
	}
}
