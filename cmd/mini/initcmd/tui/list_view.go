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
	for i, r := range l.visible() {
		start := len(lines)
		lines = append(lines, l.line(r, focused && i == l.cursor, width))
		if r.subtitle != "" {
			lines = append(lines, "      "+dim.Render(r.subtitle))
		}
		if i == l.cursor {
			first, last = start, len(lines)-1
		}
	}
	return append(lines, l.untickableLines(width)...), first, last
}

func (l *list) untickableLines(width int) []string {
	untickable := l.matching(l.untickable)
	if len(untickable) == 0 {
		return nil
	}
	var lines []string
	for _, r := range untickable {
		line := strings.TrimRight(fmt.Sprintf("%-*s  %s", width, r.label, r.detail), " ")
		lines = append(lines, cursorMark(false)+dim.Render(inMiniMark+line))
	}
	return append(lines, "", cursorMark(false)+" "+dim.Render(inMiniLegend))
}

func cursorMark(atCursor bool) string {
	if atCursor {
		return "> "
	}
	return "  "
}

const (
	inMiniMark   = " ✓  "
	inMiniLegend = "✓ already in mini"
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
