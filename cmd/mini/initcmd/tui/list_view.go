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
	if l.filtering || l.filter != "" {
		height--
	}
	lines, cursorLine, cursorHeight := l.lines()
	l.scrollTo(cursorLine, cursorHeight, height)
	shown := strings.Join(lines[l.offset:min(l.offset+height, len(lines))], "\n")
	if l.filtering || l.filter != "" {
		shown = l.filterLine() + "\n" + shown
	}
	return shown
}

func (l *list) filterLine() string {
	if l.filtering {
		return "filter: " + l.filter + "_"
	}
	return dim.Render("filter: " + l.filter)
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
	width := 0
	for _, r := range l.rows {
		width = max(width, len(r.label))
	}
	for i, r := range l.visible() {
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
