package tui

// actions are the rows a screen ends with, right under its content. The cursor reaches them by
// moving down past the content's last row, or with tab.
type actions struct {
	labels []string
	at     int
	active bool
}

func newActions(labels ...string) actions {
	return actions{labels: labels}
}

// move steps through the actions; up from the first returns the cursor to the content.
func (a *actions) move(step int) {
	switch {
	case step < 0 && a.at == 0:
		a.active = false
	default:
		a.at = min(max(a.at+step, 0), len(a.labels)-1)
	}
}

func (a *actions) reach() {
	a.active, a.at = true, 0
}

func (a *actions) toggle() {
	if a.active {
		a.active = false
		return
	}
	a.reach()
}

// lines puts a blank line between the content and the actions.
func (a *actions) lines() []string {
	lines := []string{""}
	for i, label := range a.labels {
		lines = append(lines, cursorMark(a.active && i == a.at)+label)
	}
	return lines
}
