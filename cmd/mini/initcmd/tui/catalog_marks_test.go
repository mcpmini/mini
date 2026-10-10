package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/internal/config"
)

func TestCatalogScreen_aServerTickedOnImportIsMarkedWillImportAndLosesItsCatalogTick(t *testing.T) {
	c := testCatalog()
	var imported []config.ServerConfig
	s := loadedScreen(c, func() []config.ServerConfig { return imported })
	s.handle(press("space"))

	imported = []config.ServerConfig{{Name: "gh", URL: "https://api.github.example/mcp/"}}
	s.enter()

	text := catalogText(s)
	if strings.Count(text, " +  GitHub") != 2 || !strings.Contains(text, "+ will import") ||
		strings.Contains(text, "[x]") {
		t.Errorf("screen:\n%s\nwant GitHub marked will import in both places, explained, and ticked nowhere", text)
	}
	if picks := s.picks(); len(picks) != 0 {
		t.Errorf("picks = %v; want none: the imported gh is the same URL", entryNames(picks))
	}
	imported = nil
	s.enter()
	if picks := s.picks(); len(picks) != 0 {
		t.Errorf("picks = %v; want github's catalog tick dropped once the Import row won", entryNames(picks))
	}
}

func TestCatalogScreen_aServerMiniRunsIsMarkedAndCantBeTicked(t *testing.T) {
	s := newCatalogScreen(
		catalogParams{load: fromCatalog(testCatalog()), inMini: inMiniNamed("asana"), importTicked: noImports},
	)
	s.update(s.start()())
	s.resize(120, 40)
	text := catalogText(s)
	if !strings.Contains(text, " ✓  Asana") || !strings.Contains(text, "✓ already in mini") ||
		strings.Contains(text, "will import") {
		t.Fatalf("screen:\n%s\nwant Asana marked already in mini and only that explained", text)
	}
	pressAll(s, "right", "space", "a")
	if text := catalogText(s); !strings.Contains(text, ">  ✓  Asana") || !strings.Contains(text, "[x] Linear") {
		t.Errorf("screen after →, space and a:\n%s\nwant the cursor on Asana and a ticking only Linear", text)
	}
	if got := entryNames(s.picks()); slices.Contains(got, "asana") {
		t.Errorf("picks = %v; want no asana: mini already runs it", got)
	}
}

func TestCatalogScreen_theCursorScrollsToAServerMiniRunsInAShortWindow(t *testing.T) {
	s := newCatalogScreen(
		catalogParams{load: fromCatalog(testCatalog()), inMini: inMiniNamed("linear"), importTicked: noImports},
	)
	s.update(s.start()())
	s.resize(50, 6)
	pressAll(s, "down", "right", "down", "down")
	if text := ansi.Strip(s.body(6, true)); !strings.Contains(text, ">    ✓  Linear") {
		t.Errorf("body after moving down past Asana:\n%s\nwant the cursor on Linear, scrolled into view", text)
	}
}

func TestCatalogScreen_aFilterMatchingOnlyServersMiniRunsPutsTheCursorOnOne(t *testing.T) {
	s := newCatalogScreen(
		catalogParams{load: fromCatalog(testCatalog()), inMini: inMiniNamed("asana"), importTicked: noImports},
	)
	s.update(s.start()())
	for _, width := range []int{120, 50} {
		s.resize(width, 40)
		pressAll(s, "/", "a", "s", "a", "n", "enter")
		if text := catalogText(s); !strings.Contains(text, ">  ✓  Asana") && !strings.Contains(text, ">    ✓  Asana") {
			t.Errorf("screen %d wide with filter asan:\n%s\nwant the cursor on Asana, not on its heading", width, text)
		}
		pressAll(s, "esc")
	}
}

func TestCatalogScreen_withEveryServerInMiniOrImportedIsSkipped(t *testing.T) {
	imported := func() []config.ServerConfig { return []config.ServerConfig{{Name: "linear"}, {Name: "sentry"}} }
	s := newCatalogScreen(catalogParams{
		load: fromCatalog(testCatalog()), inMini: inMiniNamed("github", "asana"), importTicked: imported,
	})
	s.update(s.start()())
	if !s.empty() {
		t.Errorf("screen:\n%s\nwant it skipped: nothing is left to tick", catalogText(s))
	}
}

func TestCatalogScreen_theCursorStartsOnTheFirstServerThatCanBeTicked(t *testing.T) {
	s := newCatalogScreen(
		catalogParams{load: fromCatalog(testCatalog()), inMini: inMiniNamed("github"), importTicked: noImports},
	)
	s.update(s.start()())
	s.resize(120, 40)
	pressAll(s, "space")
	if got := entryNames(s.picks()); len(got) != 1 || got[0] == "github" {
		t.Errorf("picks after space = %v; want one server ticked, not github: mini runs it", got)
	}
}

func TestCatalogScreen_aClosedCategoryCountsOnlyTheServersThatCanBeTicked(t *testing.T) {
	s := newCatalogScreen(
		catalogParams{load: fromCatalog(testCatalog()), inMini: inMiniNamed("asana"), importTicked: noImports},
	)
	s.update(s.start()())
	s.resize(50, 40)
	pressAll(s, "down", "space", "down", "space", "left")
	if text := catalogText(s); !strings.Contains(text, "▸ Project management\n") {
		t.Errorf("screen after space on Asana:\n%s\nwant Project management with nothing ticked: mini runs Asana", text)
	}
	pressAll(s, "space", "a", "left")
	if text := catalogText(s); !strings.Contains(text, "▸ Project management  1 ticked") {
		t.Errorf("screen after a:\n%s\nwant Project management counting Linear only", text)
	}
}
