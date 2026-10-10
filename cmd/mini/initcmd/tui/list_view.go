package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	dim  = lipgloss.NewStyle().Faint(true)
	bold = lipgloss.NewStyle().Bold(true)
)

func (l *list) view(height int, focused bool) string {
	var shown []string
	if l.header.label != "" {
		shown = append(shown, l.headerLine())
	}
	lines, first, last := l.lines(focused)
	shown = append(shown, l.scroll.cut(lines, first, last, height-len(shown))...)
	return strings.Join(shown, "\n")
}

func (l *list) filterLine() string {
	return l.filter.line()
}

func (l *list) headerLine() string {
	line := fmt.Sprintf("%-*s  %s", len(checkbox(false))+l.labelWidth(), l.header.label, l.header.detail)
	return cursorMark(false) + bold.Render(strings.TrimRight(line, " "))
}

func (l *list) labelWidth() int {
	// The first heading starts over the checkbox, so only what it overhangs widens the label column.
	labels := widest(slices.Concat(l.rows, l.untickable), func(r row) string { return r.label })
	return max(len(l.header.label)-len(checkbox(false)), labels)
}

func (l *list) lines(focused bool) (lines []string, first, last int) {
	width := l.labelWidth()
	tickable := len(l.visible())
	rows := slices.Concat(l.visible(), l.matching(l.untickable))
	for i, r := range rows {
		start := len(lines)
		lines = append(lines, l.rowLines(r, i >= tickable, focused && i == l.cursor, width)...)
		if i == l.cursor {
			first, last = start, len(lines)-1
		}
	}
	if len(rows) == tickable {
		return lines, first, last
	}
	lines = append(lines, "", legendLine(l.legend))
	// The legend can't hold the cursor, so it comes into view with the last row.
	if l.cursor == len(rows)-1 {
		last = len(lines) - 1
	}
	return lines, first, last
}

func (l *list) rowLines(r row, untickable, atCursor bool, width int) []string {
	if untickable {
		line := strings.TrimRight(fmt.Sprintf("%-*s  %s", width, r.label, r.detail), " ")
		return []string{cursorMark(atCursor) + dim.Render(doneMark+line)}
	}
	lines := []string{l.line(r, atCursor, width)}
	if r.subtitle != "" {
		lines = append(lines, "      "+dim.Render(r.subtitle))
	}
	return lines
}

func cursorMark(atCursor bool) string {
	if atCursor {
		return "> "
	}
	return "  "
}

// A legend starts one column in, so its marks line up under the marks on the rows.
func legendLine(legend string) string {
	return cursorMark(false) + " " + dim.Render(legend)
}

const (
	doneMark        = " ✓  "
	inMiniLegend    = "✓ already in mini"
	connectedLegend = "✓ already connected"
)

func checkbox(checked bool) string {
	if checked {
		return "[x] "
	}
	return "[ ] "
}

func widest[T any](items []T, text func(T) string) int {
	width := 0
	for _, item := range items {
		width = max(width, len(text(item)))
	}
	return width
}

func (l *list) line(r row, atCursor bool, width int) string {
	if r.detail == "" {
		return cursorMark(atCursor) + checkbox(l.checked[r.key]) + r.label
	}
	return cursorMark(
		atCursor,
	) + checkbox(
		l.checked[r.key],
	) + fmt.Sprintf(
		"%-*s",
		width,
		r.label,
	) + "  " + dim.Render(
		r.detail,
	)
}
