package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

func testCatalog() catalog.Catalog {
	return catalog.Catalog{
		Entries: []catalog.Entry{
			{
				Name:     "linear",
				Title:    "Linear",
				URL:      "https://mcp.linear.example/mcp",
				Category: "Project management",
				Auth:     catalog.AuthOAuth2,
			},
			{
				Name:     "sentry",
				Title:    "Sentry",
				URL:      "https://mcp.sentry.example/mcp",
				Category: "Observability",
				Auth:     catalog.AuthOAuth2,
			},
			{
				Name: "github", Title: "GitHub", URL: "https://api.github.example/mcp", Category: "Developer tools",
				Auth: catalog.AuthToken, Description: "Repositories and pull requests",
			},
			{
				Name:     "asana",
				Title:    "Asana",
				URL:      "https://mcp.asana.example/mcp",
				Category: "Project management",
				Auth:     catalog.AuthOAuth2App,
			},
		},
		Popular: []string{"github"},
	}
}

func noImports() []config.ServerConfig { return nil }

func noneInMini(catalog.Entry) bool { return false }

func inMiniNamed(names ...string) func(catalog.Entry) bool {
	return func(e catalog.Entry) bool { return slices.Contains(names, e.Name) }
}

func loadedScreen(c catalog.Catalog, imports func() []config.ServerConfig) *catalogScreen {
	s := newCatalogScreen(catalogParams{load: fromCatalog(c), inMini: noneInMini, importTicked: imports})
	s.update(s.start()())
	s.resize(120, 40)
	return s
}

func pressAll(s *catalogScreen, keys ...string) reply {
	var last reply
	for _, key := range keys {
		last, _ = s.handle(press(key))
		catalogText(s)
	}
	return last
}

func fromCatalog(c catalog.Catalog) func() (catalog.Catalog, error) {
	return func() (catalog.Catalog, error) { return c, nil }
}

func catalogText(s *catalogScreen) string {
	return ansi.Strip(s.body(30, true))
}

func entryNames(entries []catalog.Entry) []string {
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

func TestCatalogScreen_gridShowsPopularThenEachCategoryAlphabetically(t *testing.T) {
	text := catalogText(loadedScreen(testCatalog(), noImports))
	want := []string{
		"  Popular",
		"> [ ] GitHub",
		"Project management",
		"Observability",
		"Developer tools",
	}
	for _, part := range want {
		if !strings.Contains(text, part) {
			t.Errorf("screen missing %q:\n%s", part, text)
		}
	}
	if !strings.Contains(text, "Popular") || !strings.HasPrefix(strings.TrimLeft(text, " "), "Popular") {
		t.Errorf("screen:\n%s\nwant Popular first", text)
	}
	if strings.Index(text, "Asana") > strings.Index(text, "Linear") {
		t.Errorf("screen:\n%s\nwant Asana before Linear: categories list their servers by name", text)
	}
	if lines := strings.Split(text, "\n"); !strings.Contains(lines[0], "Project management") {
		t.Errorf("first line %q; want the categories side by side", lines[0])
	}
}

func TestCatalogScreen_aWindowTooSmallForTheGridOpensOneCategoryAtATime(t *testing.T) {
	for name, size := range map[string][2]int{"narrow": {50, 40}, "short": {120, 6}} {
		t.Run(name, func(t *testing.T) {
			s := loadedScreen(testCatalog(), noImports)
			s.resize(size[0], size[1])
			text := catalogText(s)
			if !strings.Contains(text, "▾ Popular\n>   [ ] GitHub") || !strings.Contains(text, "▸ Project management") {
				t.Fatalf("screen:\n%s\nwant Popular open and the other categories closed", text)
			}
			for _, key := range []string{"down", "space"} {
				s.handle(press(key))
			}
			text = catalogText(s)
			if !strings.Contains(text, "▸ Popular") || !strings.Contains(text, "▾ Project management\n    [ ] Asana") {
				t.Errorf("screen after opening Project management:\n%s\nwant it open and Popular closed", text)
			}
		})
	}
}

func TestCatalogScreen_aClosedCategorySaysHowManyOfItsServersAreTicked(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	s.resize(50, 40)
	pressAll(s, "space", "up", "space")
	if text := catalogText(s); !strings.Contains(text, "▸ Popular  1 ticked") {
		t.Errorf("screen:\n%s\nwant the closed Popular to say GitHub is ticked", text)
	}
}

func TestCatalogScreen_ticksBecomeThePicksOncePerServer(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	// GitHub under Popular, then right to Asana under Project management.
	pressAll(s, "space", "right", "enter")
	if got := entryNames(s.picks()); !slices.Equal(got, []string{"asana", "github"}) {
		t.Errorf("picks = %v, want asana and github, once each", got)
	}
	if text := catalogText(s); strings.Count(text, "[x] GitHub") != 2 {
		t.Errorf("screen:\n%s\nwant GitHub ticked under Popular and Developer tools", text)
	}
}

func TestCatalogScreen_downPastTheLastServerLeavesTheGrid(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	if got := pressAll(s, "down"); got != handled {
		t.Fatalf("down from Popular's GitHub = %v, want handled: Developer tools' GitHub is below", got)
	}
	if got := pressAll(s, "down"); got != pastLastRow || len(s.picks()) != 0 {
		t.Errorf("down past the column's last server = %v, picks %v; want past the last row with nothing ticked",
			got, s.picks())
	}
	if got := pressAll(s, "tab"); got != unhandled {
		t.Errorf("tab = %v, want it left to the app", got)
	}
}

func TestCatalogScreen_upFromContinueReturnsToTheServerTheCursorLeft(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	a := inApp(120, 40, s)
	send(a, "right", "tab")
	if view := shown(a); strings.Contains(view, "> [ ] Asana") || !strings.Contains(view, "> Continue") {
		t.Fatalf("view after tab:\n%s\nwant the cursor on Continue and none in the grid", view)
	}
	send(a, "up")
	if view := shown(a); !strings.Contains(view, "> [ ] Asana") || strings.Contains(view, "> Continue") {
		t.Errorf("view after up:\n%s\nwant the cursor back on Asana", view)
	}
}

func TestCatalogScreen_theLineUnderTheGridShowsTheHostWithinTheWindowWhateverTheDescription(t *testing.T) {
	c := testCatalog()
	c.Entries[2].Description = strings.Repeat("Repositories and pull requests, ", 4)
	s := loadedScreen(c, noImports)
	s.resize(80, 40)
	var line string
	for _, l := range strings.Split(catalogText(s), "\n") {
		if strings.Contains(l, "GitHub ·") {
			line = l
		}
	}
	// The app cuts every line at the window's edge; the host must sit before the cut.
	if cut := ansi.Truncate(
		line,
		80,
		"",
	); !strings.HasPrefix(
		strings.TrimSpace(cut),
		"GitHub · api.github.example · ",
	) {
		t.Errorf("line under the grid %q; want the host right after the name, so an 80-column window shows it", line)
	}
}

func TestCatalogScreen_filtersByDescription(t *testing.T) {
	c := testCatalog()
	s := loadedScreen(c, noImports)
	for _, key := range []string{"/", "p", "u", "l", "l"} {
		s.handle(press(key))
	}
	if text := catalogText(s); strings.Count(text, "[ ] GitHub") != 1 || strings.Contains(text, "Linear") {
		t.Errorf("screen:\n%s\nwant only github, once, matched by its description", text)
	}
	if line, keys := ansi.Strip(
		s.filterLine(),
	), s.keys(); line != "/pull_" ||
		!strings.HasPrefix(keys, "type to filter") {
		t.Errorf("filter line %q, keys %q; want the typed filter and how to end it, for the footer", line, keys)
	}
}

func TestCatalogScreen_loading(t *testing.T) {
	c := testCatalog()
	t.Run("shows loading until the catalog arrives, and isn't skipped meanwhile", func(t *testing.T) {
		s := newCatalogScreen(catalogParams{load: fromCatalog(c), inMini: noneInMini, importTicked: noImports})
		if text := catalogText(s); text != "loading…" || s.empty() {
			t.Errorf("before loading: screen %q, empty = %v; want loading… and not empty", text, s.empty())
		}
		s.update(s.start()())
		if text := catalogText(s); !strings.Contains(text, "[ ] GitHub") {
			t.Errorf("after loading:\n%s\nwant the catalog's servers", text)
		}
		if s.start() != nil {
			t.Error("start after loading returned a command; want no second fetch")
		}
	})
	t.Run("keys wait for the catalog, except going back", func(t *testing.T) {
		s := newCatalogScreen(catalogParams{load: fromCatalog(c), inMini: noneInMini, importTicked: noImports})
		a := inApp(100, 30, &fakeScreen{name: "Import", hasRows: true}, s)
		send(a, "tab", "enter", "enter", "/", "space")
		if view := shown(a); a.at != 1 || strings.Contains(view, continueLabel) || s.grid.filter.typing {
			t.Errorf(
				"view after keys while loading:\n%s\nwant the loading screen kept, no Continue to skip it, no filter",
				view,
			)
		}
		send(a, "esc")
		if a.at != 0 {
			t.Errorf("at = %d after esc while loading, want back on Import", a.at)
		}
	})
	t.Run("a catalog that can't be loaded says so", func(t *testing.T) {
		load := func() (catalog.Catalog, error) { return catalog.Catalog{}, errors.New("bad document") }
		s := newCatalogScreen(catalogParams{load: load, inMini: noneInMini, importTicked: noImports})
		s.update(s.start()())
		if text := catalogText(
			s,
		); text != "The catalog couldn't be loaded: bad document" || s.empty() ||
			s.focusable() {
			t.Errorf("screen %q, empty = %v, focusable = %v; want the error, shown, with nothing to pick",
				text, s.empty(), s.focusable())
		}
	})
}

func TestCatalogScreen_collapsibleArrowsOpenAndCloseTheCategoryUnderTheCursor(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	s.resize(50, 40)
	pressAll(s, "left")
	if text := catalogText(s); !strings.HasPrefix(text, "> ▸ Popular") {
		t.Fatalf("screen after ←:\n%s\nwant Popular closed with the cursor on its heading", text)
	}
	pressAll(s, "down", "right")
	if text := catalogText(s); !strings.Contains(text, "> ▾ Project management\n    [ ] Asana") {
		t.Errorf("screen after ↓ →:\n%s\nwant Project management open under the cursor", text)
	}
}

func TestCatalogScreen_aResizeToCollapsibleKeepsTheCursorInTheSameCategory(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	pressAll(s, "right", "right")
	if text := catalogText(s); !strings.Contains(text, "> [ ] Sentry") {
		t.Fatalf("screen:\n%s\nwant the cursor on Sentry, two columns across", text)
	}
	s.resize(50, 40)
	if text := catalogText(s); !strings.Contains(text, "> ▸ Observability") {
		t.Errorf("screen after narrowing:\n%s\nwant the cursor on Sentry's closed category", text)
	}
}

func TestCatalogScreen_spaceAfterAResizeActsWhereTheCursorNowIs(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	pressAll(s, "right", "right")
	s.resize(50, 40)
	s.handle(press("space"))
	if picks := s.picks(); len(picks) != 0 {
		t.Errorf("picks = %v; want none: Sentry's category closed, so space opened it instead", entryNames(picks))
	}
}

func TestCatalogScreen_clearingAFilterLeavesSpaceActingOnTheRowTheCursorIsOn(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	s.resize(50, 40)
	pressAll(s, "left", "/", "s", "enter", "esc", "space")
	if picks := s.picks(); len(picks) != 0 {
		t.Errorf(
			"picks = %v; want none: the cursor sat on the closed Popular heading, so space opened it",
			entryNames(picks),
		)
	}
}

func TestCatalogScreen_aLongTitleGivesWaySoTheHostShowsAtTheNarrowestWindow(t *testing.T) {
	c := testCatalog()
	c.Entries[2].Title = strings.Repeat("GitHub ", 6)[:40]
	c.Entries[2].URL = "https://attacker.example/mcp"
	s := loadedScreen(c, noImports)
	s.resize(minWidth, 40)
	var line string
	for _, l := range strings.Split(catalogText(s), "\n") {
		if strings.Contains(l, " · ") {
			line = l
		}
	}
	if !strings.Contains(ansi.Truncate(line, minWidth, ""), " · attacker.example") {
		t.Errorf("line under the grid %q; want the whole host inside a %d-column window", line, minWidth)
	}
}

func TestCatalogScreen_arrowsWhileFilteringLeaveTheOpenCategoryAsItWas(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	s.resize(50, 40)
	// "s" leaves Project management, Observability and Developer tools: → on Observability's
	// heading, second among the matches, then clear.
	pressAll(s, "/", "s", "enter", "down", "right", "esc")
	if text := catalogText(s); !strings.Contains(text, "▾ Popular") || strings.Contains(text, "▾ Project management") {
		t.Errorf("screen after clearing the filter:\n%s\nwant Popular still the open category", text)
	}
}

func TestCatalogScreen_aTicksEveryServerTheFilterShowsThenUnticksThem(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	pressAll(s, "/", "s", "enter", "a")
	if got := entryNames(s.picks()); !slices.Equal(got, []string{"asana", "sentry", "github"}) ||
		strings.Contains(catalogText(s), "[ ]") {
		t.Fatalf("picks after a with filter s = %v, want asana, sentry and github: every server it shows", got)
	}
	pressAll(s, "esc", "a")
	if got := entryNames(s.picks()); len(got) != 4 {
		t.Fatalf("picks after a with no filter = %v, want all four", got)
	}
	pressAll(s, "a")
	if got := s.picks(); len(got) != 0 {
		t.Errorf("picks after a again = %v, want none", entryNames(got))
	}
}

func TestCatalogScreen_aInTheCollapsibleLayoutTicksOnlyTheOpenCategory(t *testing.T) {
	s := loadedScreen(testCatalog(), noImports)
	s.resize(50, 40)
	pressAll(s, "a")
	if got := entryNames(s.picks()); !slices.Equal(got, []string{"github"}) {
		t.Errorf("picks after a = %v, want only github: Popular is the one category open", got)
	}
}
