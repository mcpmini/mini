package tui

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// textFilter is the "/" filter that narrows a screen's rows. While it is typed it takes every
// key, so typed characters never act as commands.
type textFilter struct {
	text   string
	typing bool
}

// filterTarget is what a filter narrows: it resets its cursor when the text changes, and still
// moves it while the filter is typed.
type filterTarget interface {
	filterChanged()
	moveKey(key string)
}

// handle takes every key while the filter is typed, and / and esc otherwise. Esc is taken only
// when it clears a filter, so with none it can go back.
func (f *textFilter) handle(key tea.KeyPressMsg, t filterTarget) bool {
	if f.typing {
		changed, move := f.typingKey(key)
		if changed {
			t.filterChanged()
		}
		if move {
			t.moveKey(key.String())
		}
		return true
	}
	switch key.String() {
	case "/":
		f.typing = true
		return true
	case "esc":
		if f.set("") {
			t.filterChanged()
			return true
		}
	}
	return false
}

func matchesFilter(filter string, texts ...string) bool {
	filter = strings.ToLower(filter)
	for _, text := range texts {
		if strings.Contains(strings.ToLower(text), filter) {
			return true
		}
	}
	return false
}

// typingKey handles a key while the filter is typed. It reports whether the text changed, and
// whether the key moves the cursor, which the screen still does.
func (f *textFilter) typingKey(key tea.KeyPressMsg) (changed, move bool) {
	switch key.String() {
	case "enter":
		f.typing = false
	case "esc":
		f.typing = false
		return f.set(""), false
	case "backspace":
		if f.text != "" {
			_, size := utf8.DecodeLastRuneInString(f.text)
			return f.set(f.text[:len(f.text)-size]), false
		}
	case "up", "down":
		return false, true
	default:
		if key.Text != "" {
			return f.set(f.text + key.Text), false
		}
	}
	return false, false
}

func (f *textFilter) set(text string) bool {
	changed := f.text != text
	f.text = text
	return changed
}

// keys replaces the screen's keys while the filter is typed, and adds how to clear one that is kept.
func (f *textFilter) keys(screenKeys string) string {
	switch {
	case f.typing:
		return "type to filter · ↑↓ move · enter done · esc clear"
	case f.text != "":
		return screenKeys + " · esc clear filter"
	}
	return screenKeys
}

func (f *textFilter) line() string {
	switch {
	case f.typing:
		return "/" + f.text + "_"
	case f.text != "":
		return dim.Render("/" + f.text)
	}
	return ""
}
