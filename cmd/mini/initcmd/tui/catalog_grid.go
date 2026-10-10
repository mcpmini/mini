package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type gridEntry struct {
	key, title, host, description string
}

func (e gridEntry) matches(filter string) bool {
	filter = strings.ToLower(filter)
	for _, text := range []string{e.key, e.title, e.host, e.description} {
		if strings.Contains(strings.ToLower(text), filter) {
			return true
		}
	}
	return false
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
	onContinue     bool
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
		if len(s.entries) > 0 {
			return gridSpot{section: i}
		}
	}
	return gridSpot{entry: -1}
}

func (g *catalogGrid) current() (gridEntry, bool) {
	shown := g.shown()
	if g.onContinue || g.at.section >= len(shown) || g.at.entry < 0 {
		return gridEntry{}, false
	}
	entries := shown[g.at.section].entries
	if g.at.entry >= len(entries) {
		return gridEntry{}, false
	}
	return entries[g.at.entry], true
}

// handle reports whether the grid took the key; enter on Continue is left to the screen.
func (g *catalogGrid) handle(key tea.KeyPressMsg) bool {
	if g.filter.typing {
		changed, move := g.filter.typingKey(key)
		if changed {
			g.filterChanged()
		}
		if move {
			g.move(key.String())
		}
		return true
	}
	switch key.String() {
	case "up", "down", "left", "right":
		g.move(key.String())
	case "tab":
		g.onContinue = !g.onContinue
	case "space", "enter":
		return g.activate(key.String())
	case "/":
		g.filter.typing, g.onContinue = true, false
	case "esc":
		if !g.filter.set("") {
			return false
		}
		g.filterChanged()
	default:
		return false
	}
	return true
}

func (g *catalogGrid) filterChanged() {
	g.at, g.onContinue, g.scroll = g.firstEntry(), false, scroll{}
}

func (g *catalogGrid) activate(key string) bool {
	if g.onContinue {
		return key != "enter"
	}
	if g.at.entry < 0 {
		g.toggleOpen(g.at.section)
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

func (g *catalogGrid) move(key string) {
	if g.onContinue {
		g.onContinue = key != "up"
		return
	}
	columns := g.columns()
	col, row := g.locate(columns)
	switch key {
	case "up", "down":
		g.moveVertically(columns[col], row, map[string]int{"up": -1, "down": 1}[key])
	case "left", "right":
		g.moveAcross(columns, col, row, map[string]int{"left": -1, "right": 1}[key])
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
		g.onContinue = true
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
	if step > 0 {
		if g.at.entry < 0 {
			g.open = g.at.section
		}
		return
	}
	if g.open == g.at.section {
		g.open = -1
	}
	g.at.entry = -1
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
