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

// view renders the list in height lines at most, scrolled so the cursor's row shows.
func (l *list) view(height int) string {
	lines, cursorLine, cursorHeight := l.lines()
	if l.filtering || l.filter != "" {
		height--
	}
	l.scrollTo(cursorLine, cursorHeight, height)
	end := min(l.offset+height, len(lines))
	shown := strings.Join(lines[l.offset:end], "\n")
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
	width := l.labelWidth()
	for i, index := range l.visible() {
		r := l.rows[index]
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
	if r.heading {
		return bold.Render(r.label)
	}
	cursor := "  "
	if atCursor {
		cursor = "> "
	}
	label := fmt.Sprintf("%-*s", width, r.label)
	if r.detail != "" {
		label += "  " + dim.Render(r.detail)
	}
	if r.disabled {
		label = dim.Render(label)
	}
	return cursor + l.box(r) + label
}

func (l *list) box(r row) string {
	switch {
	case l.checked == nil:
		return ""
	case r.disabled:
		return "    "
	case l.checked[r.key]:
		return "[x] "
	}
	return "[ ] "
}

func (l *list) labelWidth() int {
	width := 0
	for _, r := range l.rows {
		if !r.heading {
			width = max(width, len(r.label))
		}
	}
	return width
}
