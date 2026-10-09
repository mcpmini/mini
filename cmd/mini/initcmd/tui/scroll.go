package tui

// scroll keeps its offset between frames, so moving the cursor within the view doesn't scroll
// it. The app draws the footer below the body, so a taller body would hide it.
type scroll struct {
	offset int
}

// cut shows height lines with the block from line first to line last in view. A block taller than
// the window keeps its first line, so callers start the block at the line that must stay visible.
func (sc *scroll) cut(lines []string, first, last, height int) []string {
	if height <= 0 || len(lines) <= height {
		sc.offset = 0
		return lines
	}
	sc.offset = min(sc.offset, len(lines)-height)
	if last >= sc.offset+height {
		sc.offset = last + 1 - height
	}
	sc.offset = max(min(sc.offset, first), 0)
	return lines[sc.offset : sc.offset+height]
}
