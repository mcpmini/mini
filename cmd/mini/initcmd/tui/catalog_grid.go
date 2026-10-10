package tui

import (
	tea "charm.land/bubbletea/v2"
)

type gridEntry struct {
	key, title, host, description string
}

func (e gridEntry) matches(filter string) bool {
	return matchesFilter(filter, e.key, e.title, e.host, e.description)
}

type gridSection struct {
	title   string
	entries []gridEntry
	// repeated lists entries from other sections again, so the filter skips it.
	repeated bool
}

// gridSpot is a row of the grid; entry -1 is the section's heading, which only the collapsible
// layout lets the cursor rest on.
type gridSpot struct {
	section, entry int
}

// catalogGrid lays the catalog's sections out side by side when they fit the window, and
// otherwise as headings that open one at a time, so the user never scrolls a long list.
type catalogGrid struct {
	sections       []gridSection
	checked        map[string]bool
	at             gridSpot
	actions        actions
	open           int
	filter         textFilter
	width          int
	sectionsHeight int
	scroll         scroll
}

func newCatalogGrid(sections []gridSection, checked map[string]bool) *catalogGrid {
	g := &catalogGrid{sections: sections, checked: checked, actions: newActions()}
	g.at = g.firstEntry()
	return g
}

func (g *catalogGrid) shown() []gridSection {
	if g.filter.text == "" {
		return g.sections
	}
	var shown []gridSection
	for _, s := range g.sections {
		if s.repeated {
			continue
		}
		matching := gridSection{title: s.title}
		for _, e := range s.entries {
			if e.matches(g.filter.text) {
				matching.entries = append(matching.entries, e)
			}
		}
		if len(matching.entries) > 0 {
			shown = append(shown, matching)
		}
	}
	return shown
}

func (g *catalogGrid) firstEntry() gridSpot {
	for i, s := range g.shown() {
		if len(s.entries) > 0 {
			return gridSpot{section: i}
		}
	}
	return gridSpot{entry: -1}
}

func (g *catalogGrid) current() (gridEntry, bool) {
	shown := g.shown()
	if g.actions.active || g.at.section >= len(shown) || g.at.entry < 0 {
		return gridEntry{}, false
	}
	entries := shown[g.at.section].entries
	if g.at.entry >= len(entries) {
		return gridEntry{}, false
	}
	return entries[g.at.entry], true
}

// handle reports whether the grid took the key; enter on an action is left to the screen.
func (g *catalogGrid) handle(key tea.KeyPressMsg) bool {
	if g.filter.handle(key, g) {
		return true
	}
	switch key.String() {
	case "up", "down", "left", "right":
		g.moveKey(key.String())
	case "tab":
		g.actions.toggle()
	case "space", "enter":
		return g.activate(key.String())
	default:
		return false
	}
	return true
}

func (g *catalogGrid) filterChanged() {
	g.at, g.actions.active, g.scroll = g.firstEntry(), false, scroll{}
	g.settle()
}

func (g *catalogGrid) activate(key string) bool {
	if g.actions.active {
		return key != "enter"
	}
	if g.at.entry < 0 {
		// A filter shows every matching category open, so a heading has nothing to open.
		if g.filter.text == "" {
			g.toggleOpen(g.at.section)
		}
		return true
	}
	if e, ok := g.current(); ok {
		g.checked[e.key] = !g.checked[e.key]
	}
	return true
}

func (g *catalogGrid) toggleOpen(section int) {
	if g.open == section {
		g.open = -1
		return
	}
	g.open = section
}

func (g *catalogGrid) moveKey(key string) {
	if g.actions.active {
		if key == "up" || key == "down" {
			g.actions.move(direction(key))
		}
		return
	}
	columns := g.columns()
	col, row, _ := g.locate(columns)
	switch key {
	case "up", "down":
		g.moveVertically(columns[col], row, direction(key))
	case "left", "right":
		g.moveAcross(columns, col, row, direction(key))
	}
}

// moveVertically steps to the next row the cursor can rest on; past the last it reaches Continue.
func (g *catalogGrid) moveVertically(column []gridCell, row, step int) {
	for r := row + step; r >= 0 && r < len(column); r += step {
		if column[r].selectable {
			g.at = column[r].spot
			return
		}
	}
	if step > 0 {
		g.actions.reach()
	}
}

func (g *catalogGrid) moveAcross(columns [][]gridCell, col, row, step int) {
	if !g.isGrid() {
		g.openOrClose(step)
		return
	}
	next := col + step
	if next < 0 || next >= len(columns) {
		return
	}
	if r, ok := nearestSelectable(columns[next], row); ok {
		g.at = columns[next][r].spot
	}
}

// openOrClose is → and ← in the collapsible layout: → opens the section under the cursor, ← closes
// it and leaves the cursor on its heading.
func (g *catalogGrid) openOrClose(step int) {
	// A filter shows every matching category open, and numbers its sections among the matches only.
	filtered := g.filter.text != ""
	switch {
	case step > 0 && g.at.entry < 0 && !filtered:
		g.open = g.at.section
	case step < 0:
		if g.open == g.at.section && !filtered {
			g.open = -1
		}
		g.at.entry = -1
	}
}

func direction(key string) int {
	if key == "up" || key == "left" {
		return -1
	}
	return 1
}

func nearestSelectable(column []gridCell, row int) (int, bool) {
	best, found := 0, false
	for r, cell := range column {
		if cell.selectable && (!found || abs(r-row) < abs(best-row)) {
			best, found = r, true
		}
	}
	return best, found
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
