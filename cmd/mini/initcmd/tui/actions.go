package tui

import "strings"

const (
	continueLabel = "Continue"
	backLabel     = "Back"
)

// A screen the app can go back from offers Back under its choices; the first screen doesn't.
type backOfferer interface {
	offerBack(back bool)
}

// actions are the rows a screen ends with, right under its content: its choices, Continue unless
// the screen names others, then Back. The cursor reaches them by moving down past the content's
// last row, or with tab on any screen; up returns to the content.
type actions struct {
	choices []string
	back    bool
	at      int
	active  bool
}

func newActions() actions {
	return actions{choices: []string{continueLabel}}
}

func (a *actions) labels() []string {
	if a.back {
		return append(a.choices[:len(a.choices):len(a.choices)], backLabel)
	}
	return a.choices
}

func (a *actions) offerBack(back bool) {
	a.back = back
	a.at = min(a.at, len(a.labels())-1)
}

func (a *actions) setChoices(choices []string) {
	a.choices = choices
	a.at = min(a.at, len(a.labels())-1)
}

func (a *actions) onBack() bool {
	return a.back && a.at == len(a.choices)
}

// move steps through the actions; up from the first returns the cursor to the content.
func (a *actions) move(step int) {
	if step < 0 && a.at == 0 {
		a.active = false
		return
	}
	a.moveWithin(step)
}

// moveWithin steps through the actions of a screen that has nothing else to move to.
func (a *actions) moveWithin(step int) {
	a.at = min(max(a.at+step, 0), len(a.labels())-1)
}

func (a *actions) reach() {
	a.active, a.at = true, 0
}

func (a *actions) step() step {
	if a.onBack() {
		return back
	}
	return forward
}

func (a *actions) atCursor(i int) bool {
	return a.active && a.at == i
}

// lines puts a blank line above the actions. cursorLine is the line under the cursor, or -1, for a
// screen to keep in view as it scrolls.
func (a *actions) lines() (lines []string, cursorLine int) {
	lines, cursorLine = []string{""}, -1
	for i, label := range a.labels() {
		if a.atCursor(i) {
			cursorLine = len(lines)
		}
		lines = append(lines, cursorMark(a.atCursor(i))+label)
	}
	return lines, cursorLine
}

// withMessage is a screen with only a message to show above its actions.
func (a *actions) withMessage(message string) string {
	lines, _ := a.lines()
	return strings.Join(append([]string{message}, lines...), "\n")
}

// keys names ↑ even with only Continue to choose when it returns the cursor to the content.
func (a *actions) keys(upReturns bool) string {
	switch {
	case len(a.labels()) > 1:
		return "↑↓ move · enter choose"
	case upReturns:
		return "↑ move · enter continue"
	}
	return "enter continue"
}
