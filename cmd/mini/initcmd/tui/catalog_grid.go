package tui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

type gridEntry struct {
	key, title, host, description string
	state                         entryState
}

type entryState int

const (
	entryOffered entryState = iota
	entryInMini
	entryWillImport
)

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
	open           int
	filter         textFilter
	width          int
	sectionsHeight int
	scroll         scroll
}

func newCatalogGrid(sections []gridSection, checked map[string]bool) *catalogGrid {
	g := &catalogGrid{sections: sections, checked: checked}
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
		if j := slices.IndexFunc(s.entries, func(e gridEntry) bool { return e.state == entryOffered }); j >= 0 {
			return gridSpot{section: i, entry: j}
		}
	}
	return gridSpot{entry: -1}
}

func (g *catalogGrid) current() (gridEntry, bool) {
	shown := g.shown()
	if g.at.section >= len(shown) || g.at.entry < 0 {
		return gridEntry{}, false
	}
	entries := shown[g.at.section].entries
	if g.at.entry >= len(entries) {
		return gridEntry{}, false
	}
	return entries[g.at.entry], true
}

func (g *catalogGrid) handle(key tea.KeyPressMsg) reply {
	if g.filter.handle(key, g) {
		return handled
	}
	switch key.String() {
	case "up", "down", "left", "right":
		return g.moveKey(key.String())
	case "space", "enter":
		g.activate()
		return handled
	case "a":
		g.toggleAll()
		return handled
	}
	return unhandled
}

// a ticks what the layout shows: in the collapsible one, the open category's servers only.
func (g *catalogGrid) toggleAll() {
	shown := g.shown()
	var keys []string
	for _, column := range g.columns() {
		for _, cell := range column {
			if !cell.selectable || cell.spot.entry < 0 {
				continue
			}
			if e := shown[cell.spot.section].entries[cell.spot.entry]; e.state == entryOffered {
				keys = append(keys, e.key)
			}
		}
	}
	toggleAll(g.checked, keys)
}

func (g *catalogGrid) filterChanged() {
	g.at, g.scroll = g.firstEntry(), scroll{}
	g.settle()
}

func (g *catalogGrid) activate() {
	if g.at.entry < 0 {
		// A filter shows every matching category open, so a heading has nothing to open.
		if g.filter.text == "" {
			g.toggleOpen(g.at.section)
		}
		return
	}
	if e, ok := g.current(); ok && e.state == entryOffered {
		g.checked[e.key] = !g.checked[e.key]
	}
}

func (g *catalogGrid) toggleOpen(section int) {
	if g.open == section {
		g.open = -1
		return
	}
	g.open = section
}

func (g *catalogGrid) moveKey(key string) reply {
	if !g.focusable() {
		return handled
	}
	columns := g.columns()
	col, row, _ := g.locate(columns)
	if key == "up" || key == "down" {
		return g.moveVertically(columns[col], row, direction(key))
	}
	g.moveAcross(columns, col, row, direction(key))
	return handled
}

// moveVertically steps to the next row the cursor can rest on; past the last it reaches Continue.
func (g *catalogGrid) moveVertically(column []gridCell, row, step int) reply {
	for r := row + step; r >= 0 && r < len(column); r += step {
		if column[r].selectable {
			g.at = column[r].spot
			return handled
		}
	}
	if step > 0 {
		return pastLastRow
	}
	return handled
}

func (g *catalogGrid) focusable() bool {
	return g.filter.holdsCursor(g.hasSelectable())
}

func (g *catalogGrid) hasSelectable() bool {
	return slices.ContainsFunc(g.columns(), func(column []gridCell) bool {
		return slices.ContainsFunc(column, func(cell gridCell) bool { return cell.selectable })
	})
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
