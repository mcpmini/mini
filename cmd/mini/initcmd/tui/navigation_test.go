//go:build test

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
)

func TestNavigation_downPastTheLastRowOrTabReachesContinueAndUpReturns(t *testing.T) {
	for _, key := range []string{"down", "tab"} {
		t.Run(key, func(t *testing.T) {
			rows := &fakeScreen{name: "Rows", hasRows: true}
			a := inApp(100, 30, rows)
			send(a, key)
			if view := shown(
				a,
			); !strings.Contains(view, "> Continue") ||
				!strings.Contains(view, "↑ move · enter continue") {
				t.Fatalf("view after %s:\n%s\nwant the cursor on Continue and ↑ named", key, view)
			}
			send(a, "up", "space")
			if view := shown(a); strings.Contains(view, "> Continue") || rows.got[len(rows.got)-1] != "space" {
				t.Errorf("view after up:\n%s\nscreen got %v; want the cursor and keys back on the rows", view, rows.got)
			}
		})
	}
}

func TestNavigation_aScreenWithNoRowsKeepsTheCursorOnContinue(t *testing.T) {
	a := inApp(100, 30, &fakeScreen{name: "Before"}, &fakeScreen{name: "Message"})
	send(a, "enter", "up")
	if view := shown(
		a,
	); !strings.Contains(view, "> Continue\n  Back") ||
		!strings.Contains(view, "↑↓ move · enter choose") {
		t.Errorf("view after up:\n%s\nwant the cursor still on Continue: there is no row to return to", view)
	}
	a = inApp(100, 30, &fakeScreen{name: "Message"})
	if view := shown(a); !strings.Contains(view, "enter continue") || strings.Contains(view, "↑") {
		t.Errorf("view:\n%s\nwant only enter named: ↑ has nowhere to go", view)
	}
}

func TestNavigation_escGoesBackOnlyWhenTheScreenLeavesItAndThereIsAScreenBefore(t *testing.T) {
	rows := &fakeScreen{name: "Rows", hasRows: true}
	a := inApp(100, 30, &fakeScreen{name: "Before"}, rows)
	send(a, "enter")
	if send(a, "esc"); a.at != 0 || rows.got[len(rows.got)-1] != "esc" {
		t.Errorf("at = %d, screen got %v; want esc offered to the screen, then back", a.at, rows.got)
	}
}

type leavingScreen struct {
	fakeScreen
	left int
}

func (s *leavingScreen) leave() { s.left++ }

func TestNavigation_aScreenIsToldWhenTheAppLeavesItAndOnlyThen(t *testing.T) {
	first := &leavingScreen{fakeScreen: fakeScreen{name: "First"}}
	second := &leavingScreen{fakeScreen: fakeScreen{name: "Second"}}
	a := inApp(100, 30, first, second)
	send(a, "esc")
	if first.left != 0 {
		t.Errorf("left %d times after esc on the first screen; want none: the app stayed", first.left)
	}
	send(a, "enter", "esc")
	if first.left != 1 || second.left != 1 || a.at != 0 {
		t.Errorf("left First %d and Second %d times, at %d after enter then esc; want each left once, back on First",
			first.left, second.left, a.at)
	}
	send(a, "enter")
	if first.left != 2 {
		t.Errorf("left %d times after Continue took the app off First again, want 2", first.left)
	}
}

type finishingScreen struct {
	fakeScreen
	done bool
}

func (s *finishingScreen) finished() bool { return s.done }

func TestNavigation_aFinishedScreenPutsTheCursorOnContinueUntilTheUserMovesIt(t *testing.T) {
	s := &finishingScreen{fakeScreen: fakeScreen{name: "Logins", hasRows: true}}
	a := inApp(100, 30, s)
	if view := shown(a); strings.Contains(view, "> Continue") {
		t.Fatalf("view:\n%s\nwant the cursor on the rows while there is work left", view)
	}
	s.done = true
	if view := shown(a); !strings.Contains(view, "> Continue") {
		t.Fatalf("view once finished:\n%s\nwant the cursor on Continue", view)
	}
	send(a, "up")
	if view := shown(a); strings.Contains(view, "> Continue") {
		t.Errorf("view after up:\n%s\nwant the cursor back on the rows: the user moved it", view)
	}
}

type choosingScreen struct {
	fakeScreen
	ready  bool
	chosen int
}

func (s *choosingScreen) choices() []string          { return []string{"Keep", "Drop"} }
func (s *choosingScreen) choiceLines(i int) []string { return []string{"  why " + s.choices()[i]} }

func (s *choosingScreen) choose(i int) bool {
	s.chosen = i
	return s.ready
}

func TestNavigation_aScreenWithChoicesStartsOnThemAndMovesOnOnceOneIsMade(t *testing.T) {
	s := &choosingScreen{fakeScreen: fakeScreen{name: "Pick", hasRows: true}}
	a := inApp(100, 30, &fakeScreen{name: "Before"}, s)
	send(a, "enter")
	want := "> Keep\n  why Keep\n  Drop\n  why Drop\n\n  Back"
	if view := shown(a); !strings.Contains(view, want) {
		t.Fatalf(
			"view:\n%s\nwant the choices with their lines, the cursor on the first, and Back apart:\n%s",
			view,
			want,
		)
	}
	if send(a, "down", "enter"); a.at != 1 || s.chosen != 1 {
		t.Fatalf("at %d, chosen %d; want Drop tried and the screen kept: it isn't ready", a.at, s.chosen)
	}
	s.ready = true
	if cmd := send(a, "enter"); cmd == nil {
		t.Error("enter on a choice the screen accepts = nil, want the app to finish")
	}
}

type waitingScreen struct {
	fakeScreen
	loading bool
}

func (s *waitingScreen) waiting() bool { return s.loading }

func TestNavigation_aWaitingScreenOffersNothingToChooseButEscStillGoesBack(t *testing.T) {
	s := &waitingScreen{fakeScreen: fakeScreen{name: "Catalog"}, loading: true}
	a := inApp(100, 30, &fakeScreen{name: "Before"}, s, &fakeScreen{name: "After"})
	send(a, "enter", "enter", "tab")
	if view := shown(a); a.at != 1 || strings.Contains(view, continueLabel) || len(s.got) != 0 {
		t.Fatalf("view:\n%s\nat %d, screen got %v; want the screen kept, no Continue drawn, no keys passed on",
			view, a.at, s.got)
	}
	send(a, "esc")
	if a.at != 0 {
		t.Errorf("at = %d after esc while waiting, want back on Before", a.at)
	}
}

func TestNavigation_theCursorOnBackMovesToTheLastRowLeftWhenBackGoes(t *testing.T) {
	before := &fakeScreen{name: "Before"}
	a := inApp(100, 30, before, &fakeScreen{name: "After"})
	send(a, "enter", "down")
	before.nothing = true
	a.Update(checkFinished{})
	if view := shown(a); !strings.Contains(view, "> Continue") {
		t.Errorf("view after Back went away under the cursor:\n%s\nwant the cursor on Continue", view)
	}
}

func TestNavigation_theRowsUnderTheCatalogFitTheWindowWithBack(t *testing.T) {
	s := loadedScreen(longCatalog(40), noImports)
	a := inApp(minWidth, 30, &fakeScreen{name: "Import"}, s)
	send(a, "enter")
	if lines := strings.Count(shown(a), "\n") + 1; lines != 30 {
		t.Errorf("view has %d lines in a 30-line window, want 30: the heading or footer is cut otherwise", lines)
	}
}

func longCatalog(entries int) catalog.Catalog {
	var c catalog.Catalog
	for i := range entries {
		name := fmt.Sprintf("server-%02d", i)
		c.Entries = append(c.Entries, catalog.Entry{
			Name: name, URL: "https://" + name + ".example/mcp", Category: "Tools", Auth: catalog.AuthNone,
		})
	}
	return c
}

func TestNavigation_aFilterThatMatchesNothingKeepsItsKeys(t *testing.T) {
	t.Run("on Import, the first screen", func(t *testing.T) {
		imports := importScreenFor(
			[]initcmd.Candidate{candidate("github", "https://gh.example.com/mcp", true, "Codex")},
		)
		a := inApp(100, 30, imports, &fakeScreen{name: "After"})
		send(a, "/", "z", "backspace", "x")
		if a.at != 0 || imports.list.filter.text != "x" {
			t.Fatalf("at %d, filter %q after /z, backspace, x; want the filter edited to x on Import",
				a.at, imports.list.filter.text)
		}
		send(a, "enter", "esc")
		if view := shown(a); a.at != 0 || imports.list.filter.active() || !strings.Contains(view, "> [x] github") {
			t.Errorf("view after enter and esc:\n%s\nwant the filter cleared and the cursor back on github", view)
		}
	})
	t.Run("on Catalog, esc clears it instead of going back", func(t *testing.T) {
		s := loadedScreen(testCatalog(), noImports)
		a := inApp(120, 40, &fakeScreen{name: "Import"}, s)
		send(a, "enter", "/", "q", "q", "esc")
		if a.at != 1 || s.grid.filter.active() {
			t.Errorf("at %d, filter %q; want Catalog kept with the filter cleared", a.at, s.grid.filter.text)
		}
	})
}

func TestNavigation_aKeyOnContinueKeepsTheCursorThereWhenRowsAppear(t *testing.T) {
	s := &finishingScreen{fakeScreen: fakeScreen{name: "Logins", hasRows: true}, done: true}
	a := inApp(100, 30, &fakeScreen{name: "Before"}, s)
	send(a, "enter", "down", "up")
	s.done = false
	if view := shown(a); !strings.Contains(view, "> Continue") {
		t.Errorf("view after a new login appeared:\n%s\nwant the cursor still on Continue, where the user put it", view)
	}
}

func TestNavigation_upOnContinueWithNoRowsLeavesTheCursorThereWhenRowsAppear(t *testing.T) {
	s := &fakeScreen{name: "Logins"}
	a := inApp(100, 30, &fakeScreen{name: "Before"}, s)
	send(a, "enter", "up")
	s.hasRows = true
	if view := shown(a); !strings.Contains(view, "> Continue") {
		t.Errorf("view after rows appeared:\n%s\nwant the cursor still on Continue: up found no row to go to", view)
	}
}
