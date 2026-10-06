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
	lines := l.lines()
	l.scrollTo(height)
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
	for i, r := range l.visible() {
		cursor, box := "  ", "[ ] "
		if i == l.cursor {
			cursor = "> "
		}
		if l.checked[r.key] {
			box = "[x] "
		}
		line := cursor + box + fmt.Sprintf("%-*s", width, r.label)
		if r.detail != "" {
			line += "  " + dim.Render(r.detail)
		}
		lines = append(lines, line)
	}
	return lines
}
