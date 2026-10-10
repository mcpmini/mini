package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	dim  = lipgloss.NewStyle().Faint(true)
	bold = lipgloss.NewStyle().Bold(true)
)

func (l *list) view(height int) string {
	var shown []string
	if l.header.label != "" {
		shown = append(shown, l.headerLine())
	}
	// The actions stay in view however far the rows scroll.
	actions, _ := l.actions.lines()
	height -= len(shown) + len(actions)
	lines, first, last := l.lines()
	shown = append(shown, l.scroll.cut(lines, first, last, height)...)
	return strings.Join(append(shown, actions...), "\n")
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
	return max(len(l.header.label)-len(checkbox(false)), widest(l.rows, func(r row) string { return r.label }))
}

func (l *list) lines() (lines []string, first, last int) {
	width := l.labelWidth()
	for i, r := range l.visible() {
		start := len(lines)
		lines = append(lines, l.line(r, i == l.cursor && !l.actions.active, width))
		if r.subtitle != "" {
			lines = append(lines, "      "+dim.Render(r.subtitle))
		}
		if i == l.cursor {
			first, last = start, len(lines)-1
		}
	}
	return lines, first, last
}

func cursorMark(atCursor bool) string {
	if atCursor {
		return "> "
	}
	return "  "
}

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
	line := cursorMark(atCursor) + checkbox(l.checked[r.key]) + fmt.Sprintf("%-*s", width, r.label)
	if r.detail != "" {
		line += "  " + dim.Render(r.detail)
	}
	return line
}
