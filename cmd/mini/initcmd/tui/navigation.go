package tui

import tea "charm.land/bubbletea/v2"

const (
	continueLabel = "Continue"
	backLabel     = "Back"
	// The rows under a screen with no choices of its own: a blank line, Continue and Back.
	tallestNavigation = 3
)

type chooser interface {
	choices() []string
	choiceLines(choice int) []string
	choose(choice int) (chosen bool)
}

type finisher interface {
	finished() bool
}

// A disabled choice is drawn dim and never chosen; the cursor still stops on it, so it reads as a choice.
type disabler interface {
	disabled(choice int) bool
}

// A screen Continue can open says what opening it does, for the Continue row before it.
type destination interface {
	arrivalHint() string
}

type focus int

const (
	// Until the user moves the cursor onto or off the navigation, a finished screen starts it there.
	focusUnmoved focus = iota
	focusRows
	focusNavigation
)

type navigation struct {
	focus focus
	at    int
	// Until the user moves it, the cursor is the default pick and follows which choices can be taken.
	moved  bool
	scroll scroll
}

func (a *app) labels() []string {
	labels := []string{continueLabel}
	if c, ok := a.current().(chooser); ok {
		labels = c.choices()
	}
	if a.canGoBack() {
		return append(labels[:len(labels):len(labels)], backLabel)
	}
	return labels
}

func (a *app) onNavigation() bool {
	if !a.current().focusable() {
		return true
	}
	switch a.nav.focus {
	case focusRows:
		return false
	case focusNavigation:
		return true
	}
	f, ok := a.current().(finisher)
	return ok && f.finished()
}

func (a *app) onBack(at int) bool {
	return a.canGoBack() && at == len(a.labels())-1
}

func (a *app) disabledChoice(i int) bool {
	d, ok := a.current().(disabler)
	return ok && !a.onBack(i) && d.disabled(i)
}

func (a *app) settleDefaultChoice() {
	if a.nav.moved || !a.disabledChoice(a.nav.at) {
		return
	}
	for i := range a.labels() {
		if !a.disabledChoice(i) {
			a.nav.at = i
			return
		}
	}
}

// Back goes away under the cursor when the screens before this one empty.
func (a *app) clampNavigationCursor() {
	a.nav.at = min(a.nav.at, len(a.labels())-1)
}

func (a *app) reachNavigation() {
	a.nav.focus, a.nav.at = focusNavigation, 0
}

// A key on the navigation makes it the user's choice, so a row that appears later doesn't take the
// cursor from it.
func (a *app) handleNavigation(key tea.KeyPressMsg) tea.Cmd {
	a.nav.focus = focusNavigation
	switch key.String() {
	case "up":
		a.nav.moved = true
		a.moveUpNavigation()
	case "down":
		a.nav.moved, a.nav.at = true, min(a.nav.at+1, len(a.labels())-1)
	case "tab":
		a.nav.moved, a.nav.at = true, 0
	case "enter":
		return a.act()
	case "esc":
		return a.back()
	}
	return nil
}

func (a *app) moveUpNavigation() {
	if a.nav.at > 0 {
		a.nav.at--
		return
	}
	if a.current().focusable() {
		a.nav.focus = focusRows
	}
}

func (a *app) act() tea.Cmd {
	if a.onBack(a.nav.at) {
		return a.back()
	}
	if a.disabledChoice(a.nav.at) {
		return nil
	}
	if c, ok := a.current().(chooser); ok && !c.choose(a.nav.at) {
		return nil
	}
	return a.forward()
}

// first and last bound the cursor's row and the lines under it, or are -1 when the cursor is
// elsewhere.
func (a *app) navigationLines() (lines []string, first, last int) {
	c, choosing := a.current().(chooser)
	lines, first, last = []string{""}, -1, -1
	for i, label := range a.labels() {
		var under []string
		if choosing && !a.onBack(i) {
			under = c.choiceLines(i)
		}
		atCursor := a.onNavigation() && a.nav.at == i
		start := len(lines)
		lines = append(append(lines, cursorMark(atCursor)+a.navigationText(i, label)), under...)
		if atCursor {
			first, last = start, len(lines)-1
		}
	}
	return lines, first, last
}

func (a *app) navigationText(i int, label string) string {
	if a.disabledChoice(i) {
		return dim.Render(label)
	}
	if label != continueLabel {
		return label
	}
	if hint := a.arrivalHint(); hint != "" {
		return label + "   " + dim.Render(hint)
	}
	return label
}

// When Continue crosses the save point, the next screen depends on what the save writes, so no hint could be relied on.
func (a *app) arrivalHint() string {
	next, ok := a.nextShown(a.at, 1)
	if !ok || a.saves.crossedBy(a.at, next) {
		return ""
	}
	if d, ok := a.screens[next].(destination); ok {
		return d.arrivalHint()
	}
	return ""
}

func (a *app) navigationKeys() string {
	switch {
	case a.disabledChoice(a.nav.at):
		return "↑↓ move"
	case len(a.labels()) > 1:
		return "↑↓ move · enter choose"
	case a.current().focusable():
		return "↑ move · enter continue"
	}
	return "enter continue"
}
