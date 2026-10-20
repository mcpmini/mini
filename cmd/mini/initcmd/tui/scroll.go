package tui

// scroll keeps its offset between frames, so moving the cursor within the view doesn't scroll
// it. The app draws the footer below the body, so a taller body would hide it.
type scroll struct {
	offset int
}

// cut shows height lines with the block from line first to line last in view. A block taller than
// the window keeps its first line, so callers start the block at the line that must stay visible.
func (sc *scroll) cut(params cutParams) []string {
	if params.Height <= 0 || len(params.Lines) <= params.Height {
		sc.offset = 0
		return params.Lines
	}
	sc.offset = min(sc.offset, len(params.Lines)-params.Height)
	if params.Last >= sc.offset+params.Height {
		sc.offset = params.Last + 1 - params.Height
	}
	sc.offset = max(min(sc.offset, params.First), 0)
	return params.Lines[sc.offset : sc.offset+params.Height]
}

type cutParams struct {
	Lines  []string
	First  int
	Last   int
	Height int
}
