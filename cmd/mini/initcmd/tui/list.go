package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// row is one line of a list. Rows sharing a key share one tick, so a server listed twice (once
// under Popular, once under its category) is ticked or unticked as one.
type row struct {
	key      string
	label    string
	detail   string
	subtitle string
	heading  bool
	disabled bool
}

func (r row) selectable() bool {
	return !r.heading && !r.disabled
}

func (r row) matches(filter string) bool {
	filter = strings.ToLower(filter)
	return strings.Contains(strings.ToLower(r.label), filter) || strings.Contains(strings.ToLower(r.detail), filter)
}

// list is the one list component every screen uses. checked is nil for a list without checkboxes.
type list struct {
	rows      []row
	checked   map[string]bool
	cursor    int
	offset    int
	filter    string
	filtering bool
}

func newList(rows []row, checked map[string]bool) *list {
	l := &list{rows: rows, checked: checked}
	l.cursor = l.next(-1, 1)
	return l
}

// visible are the indexes of the rows shown. While filtering, each key is shown once and a heading
// only when a row under it is shown.
func (l *list) visible() []int {
	if l.filter == "" {
		return indexes(len(l.rows))
	}
	var shown []int
	seen := map[string]bool{}
	heading := -1
	for i, r := range l.rows {
		switch {
		case r.heading:
			heading = i
		case r.matches(l.filter) && !seen[r.key]:
			seen[r.key] = true
			if heading >= 0 {
				shown, heading = append(shown, heading), -1
			}
			shown = append(shown, i)
		}
	}
	return shown
}

func indexes(n int) []int {
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	return all
}

// next is the position in visible() of the first selectable row from position from, stepping by
// step; -1 when there is none.
func (l *list) next(from, step int) int {
	shown := l.visible()
	for i := from + step; i >= 0 && i < len(shown); i += step {
		if l.rows[shown[i]].selectable() {
			return i
		}
	}
	return -1
}

func (l *list) current() (row, bool) {
	shown := l.visible()
	if l.cursor < 0 || l.cursor >= len(shown) {
		return row{}, false
	}
	return l.rows[shown[l.cursor]], true
}

func (l *list) move(step int) {
	if i := l.next(l.cursor, step); i >= 0 {
		l.cursor = i
	}
}

func (l *list) toggle() {
	if r, ok := l.current(); ok && l.checked != nil {
		l.checked[r.key] = !l.checked[r.key]
	}
}

// toggleAll ticks every selectable row, or unticks them all when they are all ticked already.
func (l *list) toggleAll() {
	all := true
	for _, r := range l.rows {
		if r.selectable() && !l.checked[r.key] {
			all = false
		}
	}
	for _, r := range l.rows {
		if r.selectable() {
			l.checked[r.key] = !all
		}
	}
}

func (l *list) setFilter(filter string) {
	l.filter = filter
	l.cursor, l.offset = l.next(-1, 1), 0
}

// handle takes the keys the list owns and reports whether it took this one. In filter mode it
// takes every key, so typed characters never act as commands.
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
