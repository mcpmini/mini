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
	l.scrollTo(height)
	lines := l.lines()
	return strings.Join(lines[l.offset:min(l.offset+height, len(lines))], "\n")
}

func (l *list) scrollTo(height int) {
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+height {
		l.offset = l.cursor - height + 1
	}
	l.offset = max(l.offset, 0)
}

func (l *list) lines() []string {
	width := 0
	for _, r := range l.rows {
		width = max(width, len(r.label))
	}
	var lines []string
	for i, r := range l.rows {
		lines = append(lines, l.line(r, i == l.cursor, width))
	}
	return lines
}

func (l *list) line(r row, atCursor bool, width int) string {
	cursor, box := "  ", "[ ] "
	if atCursor {
		cursor = "> "
	}
	if l.checked[r.key] {
		box = "[x] "
	}
	line := cursor + box + fmt.Sprintf("%-*s", width, r.label)
	if r.detail != "" {
		line += "  " + dim.Render(r.detail)
	}
	return line
}
