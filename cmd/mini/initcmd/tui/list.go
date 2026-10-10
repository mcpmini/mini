package tui

import (
	"slices"

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
	rows       []row
	untickable []row
	checked    map[string]bool
	cursor     int
	scroll     scroll
	// filterable lets / start a filter; a short list, like Connect's agents, has no need of one.
	filterable bool
	filter     textFilter
	// header names the columns; it stays above the rows as they scroll. No label means no header.
	header row
}

func newList(rows []row, checked map[string]bool) *list {
	return &list{rows: rows, checked: checked}
}

func (l *list) visible() []row {
	return l.matching(l.rows)
}

func (l *list) matching(rows []row) []row {
	if l.filter.text == "" {
		return rows
	}
	var shown []row
	for _, r := range rows {
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

func (l *list) move(step int) reply {
	if step > 0 && l.cursor >= len(l.visible())-1 {
		return pastLastRow
	}
	l.cursor = min(max(l.cursor+step, 0), max(len(l.visible())-1, 0))
	return handled
}

func (l *list) toggle() {
	if r, ok := l.current(); ok {
		l.checked[r.key] = !l.checked[r.key]
	}
}

func (l *list) toggleAll() {
	var keys []string
	for _, r := range l.visible() {
		keys = append(keys, r.key)
	}
	toggleAll(l.checked, keys)
}

// toggleAll ticks every key the user can see, or unticks them all when they are all ticked. Rows a
// filter hides keep their ticks: the user can't see them change.
func toggleAll(checked map[string]bool, shown []string) {
	all := !slices.ContainsFunc(shown, func(key string) bool { return !checked[key] })
	for _, key := range shown {
		checked[key] = !all
	}
}

func (l *list) filterChanged() {
	l.cursor, l.scroll = 0, scroll{}
}

func (l *list) keys(screenKeys string) string {
	return l.filter.keys(screenKeys)
}

func (l *list) handle(key tea.KeyPressMsg) reply {
	if l.filterable && l.filter.handle(key, l) {
		return handled
	}
	switch key.String() {
	case "up", "down":
		return l.moveKey(key.String())
	case "space", "enter":
		l.toggle()
		return handled
	case "a":
		l.toggleAll()
		return handled
	}
	return unhandled
}

func (l *list) moveKey(key string) reply {
	return l.move(direction(key))
}

func (l *list) focusable() bool {
	return l.filter.holdsCursor(len(l.visible()) > 0)
}
