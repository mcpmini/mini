package tui

const (
	continueLabel = "Continue →"
	backLabel     = "← Back"
)

// A screen the app can go back from offers Back under Continue; the first screen offers only Continue.
type backOfferer interface {
	offerBack(back bool)
}

func actionLabels(back bool) []string {
	if back {
		return []string{continueLabel, backLabel}
	}
	return []string{continueLabel}
}

func actionStep(label string) step {
	if label == backLabel {
		return back
	}
	return forward
}

// actionLines puts a blank line above the actions; at is the action under the cursor, or -1.
func actionLines(labels []string, at int) []string {
	lines := []string{""}
	for i, label := range labels {
		lines = append(lines, cursorMark(i == at)+label)
	}
	return lines
}

// actions are the rows a screen ends with, right under its content. The cursor reaches them by
// moving down past the content's last row, or with tab.
type actions struct {
	labels []string
	at     int
	active bool
}

func newActions() actions {
	return actions{labels: actionLabels(false)}
}

func (a *actions) offerBack(back bool) {
	a.labels = actionLabels(back)
	a.at = min(a.at, len(a.labels)-1)
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

// moveWithin steps through the actions of a screen that has nothing else to move to.
func (a *actions) moveWithin(step int) {
	a.at = min(max(a.at+step, 0), len(a.labels)-1)
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

func (a *actions) step() step {
	return actionStep(a.labels[a.at])
}

func (a *actions) lines() []string {
	at := -1
	if a.active {
		at = a.at
	}
	return actionLines(a.labels, at)
}
