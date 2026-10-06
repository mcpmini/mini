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
	height -= len(shown)
	lines, cursorLine, cursorHeight := l.lines()
	l.scrollTo(cursorLine, cursorHeight, height)
	return strings.Join(append(shown, lines[l.offset:min(l.offset+height, len(lines))]...), "\n")
}

func (l *list) filterLine() string {
	switch {
	case l.filtering:
		return "/" + l.filter + "_"
	case l.filter != "":
		return dim.Render("/" + l.filter)
	}
	return ""
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
	section := ""
	for i, r := range l.visible() {
		if r.section != section && len(lines) > 0 {
			lines = append(lines, "")
		}
		start := len(lines)
		if r.section != section {
			lines = append(lines, bold.Render(r.section))
			section = r.section
		}
		if i == l.cursor {
			// The block starts at the heading, so scrolling up to a section's first row shows its heading.
			cursorLine, cursorHeight = start, len(lines)-start+1
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
