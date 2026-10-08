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
	if l.header.label != "" {
		height--
	}
	lines, cursorLine, cursorHeight := l.lines()
	l.scrollTo(cursorLine, cursorHeight, height)
	shown := strings.Join(lines[l.offset:min(l.offset+height, len(lines))], "\n")
	if l.header.label != "" {
		shown = l.headerLine() + "\n" + shown
	}
	return shown
}

func (l *list) headerLine() string {
	line := fmt.Sprintf("%-*s  %s", l.labelWidth(), l.header.label, l.header.detail)
	return "      " + bold.Render(strings.TrimRight(line, " "))
}

func (l *list) labelWidth() int {
	width := len(l.header.label)
	for _, r := range l.rows {
		width = max(width, len(r.label))
	}
	return width
}

func (l *list) scrollTo(line, rowHeight, height int) {
	if line < l.offset {
		l.offset = line
	}
	if line+rowHeight > l.offset+height {
		l.offset = line + rowHeight - height
	}
	l.offset = max(l.offset, 0)
}

func (l *list) lines() (lines []string, cursorLine, cursorHeight int) {
	width := l.labelWidth()
	for i, r := range l.rows {
		if i == l.cursor {
			cursorLine, cursorHeight = len(lines), 1
		}
		lines = append(lines, l.line(r, i == l.cursor, width))
		if r.subtitle != "" {
			lines = append(lines, "      "+dim.Render(r.subtitle))
			if i == l.cursor {
				cursorHeight++
			}
		}
	}
	return lines, cursorLine, cursorHeight
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
