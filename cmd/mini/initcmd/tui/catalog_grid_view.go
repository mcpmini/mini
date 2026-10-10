package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	gridColumns = 3
	columnGap   = 5
	// Below the sections: a blank line, the entry under the cursor, a blank line and Continue.
	linesUnderSections = 4
	// The app's footer at its tallest: a filter line, and keys wrapped onto two lines.
	tallestFooter = 3
)

// The layout follows the window, not the frame: the footer grows a line when its keys wrap, and a
// layout picked from the height left over would switch whenever the keys it shows do.
func (g *catalogGrid) resize(width, windowHeight int) {
	g.width = width
	g.sectionsHeight = windowHeight - headingLines - blankLinesAroundBody - tallestFooter - linesUnderSections
	g.settle()
}

type gridCell struct {
	text       string
	spot       gridSpot
	selectable bool
}

// A 2-column grid would still scroll, so a window too small for 3 columns gets collapsible sections.
func (g *catalogGrid) isGrid() bool {
	columns := g.gridColumns()
	width := (len(columns) - 1) * columnGap
	height := 0
	for _, column := range columns {
		width += columnWidth(column)
		height = max(height, len(column))
	}
	return width <= g.width && height <= g.sectionsHeight
}

func (g *catalogGrid) columns() [][]gridCell {
	if g.isGrid() {
		return g.gridColumns()
	}
	return [][]gridCell{g.collapsibleColumn()}
}

// gridColumns stacks each section into the shortest column, so the columns end close together.
func (g *catalogGrid) gridColumns() [][]gridCell {
	columns := make([][]gridCell, gridColumns)
	for i, s := range g.shown() {
		shortest := 0
		for c := range columns {
			if len(columns[c]) < len(columns[shortest]) {
				shortest = c
			}
		}
		if len(columns[shortest]) > 0 {
			columns[shortest] = append(columns[shortest], gridCell{spot: gridSpot{section: -1}})
		}
		columns[shortest] = append(
			columns[shortest],
			gridCell{text: bold.Render(s.title), spot: gridSpot{section: i, entry: -1}},
		)
		columns[shortest] = append(columns[shortest], g.entryCells(i, s, "")...)
	}
	return columns
}

func (g *catalogGrid) collapsibleColumn() []gridCell {
	var column []gridCell
	for i, s := range g.shown() {
		open := g.open == i || g.filter.text != ""
		column = append(
			column,
			gridCell{text: g.collapsibleHeading(s, open), spot: gridSpot{section: i, entry: -1}, selectable: true},
		)
		if open {
			column = append(column, g.entryCells(i, s, "  ")...)
		}
	}
	return column
}

func (g *catalogGrid) collapsibleHeading(s gridSection, open bool) string {
	if open {
		return "▾ " + bold.Render(s.title)
	}
	ticked := 0
	for _, e := range s.entries {
		if g.checked[e.key] {
			ticked++
		}
	}
	if ticked == 0 {
		return "▸ " + bold.Render(s.title)
	}
	return "▸ " + bold.Render(s.title) + "  " + dim.Render(fmt.Sprintf("%d ticked", ticked))
}

func (g *catalogGrid) entryCells(section int, s gridSection, indent string) []gridCell {
	cells := make([]gridCell, 0, len(s.entries))
	for i, e := range s.entries {
		text := indent + checkbox(g.checked[e.key]) + e.title
		cells = append(cells, gridCell{text: text, spot: gridSpot{section: section, entry: i}, selectable: true})
	}
	return cells
}

func columnWidth(column []gridCell) int {
	width := 0
	for _, cell := range column {
		width = max(width, lipgloss.Width(cursorMark(false)+cell.text))
	}
	return width
}

// locate finds the cursor's cell. A cursor the layout no longer shows stands on the nearest row it
// can rest on; settle moves it there.
func (g *catalogGrid) locate(columns [][]gridCell) (col, row int, at gridSpot) {
	for c, column := range columns {
		for r, cell := range column {
			if cell.selectable && cell.spot == g.at {
				return c, r, cell.spot
			}
		}
	}
	for c, column := range columns {
		for r, cell := range column {
			if cell.selectable && cell.spot.section == g.at.section {
				return c, r, cell.spot
			}
		}
	}
	return 0, 0, g.firstEntry()
}

// settle runs whenever what the layout shows changes, so drawing a frame never moves the cursor.
func (g *catalogGrid) settle() {
	_, _, g.at = g.locate(g.columns())
}

func (g *catalogGrid) view(height int) string {
	columns := g.columns()
	col, row, _ := g.locate(columns)
	lines := joinColumns(columns, col, row, !g.actions.active)
	first, last := row, row
	if g.isGrid() {
		first, last = 0, 0
	}
	lines = g.scroll.cut(lines, first, last, height-linesUnderSections)
	lines = append(lines, "", g.detail())
	return strings.Join(append(lines, g.actions.lines()...), "\n")
}

func joinColumns(columns [][]gridCell, col, row int, showCursor bool) []string {
	height := 0
	for _, column := range columns {
		height = max(height, len(column))
	}
	widths := make([]int, len(columns))
	for c, column := range columns {
		widths[c] = columnWidth(column)
	}
	lines := make([]string, height)
	for r := range lines {
		var line strings.Builder
		for c, column := range columns {
			cell := ""
			if r < len(column) {
				cell = cursorMark(showCursor && c == col && r == row) + column[r].text
			}
			line.WriteString(cell + strings.Repeat(" ", widths[c]-ansi.StringWidth(cell)+columnGap))
		}
		lines[r] = strings.TrimRight(line.String(), " ")
	}
	return lines
}

// The grid shows only names, so the line under it gives the host of the entry under the cursor:
// the only entry space or enter can tick, so no fetched name can pass for another service unseen.
func (g *catalogGrid) detail() string {
	e, ok := g.current()
	if !ok {
		return ""
	}
	// The app cuts lines at the window's edge: the host comes before the description, and the
	// title gives way, so the host always shows.
	title := ansi.Truncate(e.title, max(g.width-ansi.StringWidth("  "+" · "+e.host), 1), "…")
	parts := slices.DeleteFunc([]string{title, e.host, e.description}, func(p string) bool { return p == "" })
	return "  " + dim.Render(strings.Join(parts, " · "))
}

func (g *catalogGrid) keys(screenKeys string) string {
	return g.filter.keys(screenKeys)
}
