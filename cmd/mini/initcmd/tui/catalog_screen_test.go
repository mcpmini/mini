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
				URL:      "https://mcp.linear.example/mcp",
				Category: "Project management",
				Auth:     catalog.AuthOAuth2,
			},
			{
				Name:     "sentry",
				URL:      "https://mcp.sentry.example/mcp",
				Category: "Observability",
				Auth:     catalog.AuthOAuth2,
			},
			{
				Name: "github", URL: "https://api.github.example/mcp", Category: "Developer tools",
				Auth: catalog.AuthToken, Description: "Repositories and pull requests",
			},
			{
				Name:     "asana",
				URL:      "https://mcp.asana.example/mcp",
				Category: "Project management",
				Auth:     catalog.AuthOAuth2App,
			},
		},
		Popular: []string{"github"},
	}
}

func noImports() []config.ServerConfig { return nil }

func offerAll(entries []catalog.Entry) []catalog.Entry { return entries }

func loadedScreen(c catalog.Catalog, imports func() []config.ServerConfig) *catalogScreen {
	s := newCatalogScreen(catalogParams{load: fromCatalog(c), offered: offerAll, imports: imports})
	s.update(s.start()())
	return s
}

func fromCatalog(c catalog.Catalog) func() (LoadedCatalog, error) {
	return func() (LoadedCatalog, error) { return LoadedCatalog{Catalog: c}, nil }
}

func catalogText(s *catalogScreen) string {
	return ansi.Strip(s.body(30))
}

func entryNames(entries []catalog.Entry) []string {
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

func TestCatalogScreen_listsPopularThenEachCategoryInCatalogOrder(t *testing.T) {
	c := testCatalog()
	text := catalogText(loadedScreen(c, noImports))
	want := []string{
		"  SERVER      URL\nPopular\n> [ ] github  api.github.example\n",
		"Project management\n  [ ] linear  mcp.linear.example\n  [ ] asana   mcp.asana.example\n",
		"Observability\n  [ ] sentry  mcp.sentry.example\n",
		"Developer tools\n  [ ] github  api.github.example",
	}
	for _, part := range want {
		if !strings.Contains(text, part) {
			t.Errorf("screen missing %q:\n%s", part, text)
		}
	}
	order := []int{
		strings.Index(
			text,
			"Project management",
		),
		strings.Index(text, "Observability"),
		strings.Index(text, "Developer tools"),
	}
	if !slices.IsSorted(order) {
		t.Errorf("screen:\n%s\nwant categories in the order the catalog first lists them", text)
	}
}

func TestCatalogScreen_ticksBecomeThePicksOncePerServer(t *testing.T) {
	c := testCatalog()
	s := loadedScreen(c, noImports)
	for _, key := range []string{"space", "down", "down", "space"} {
		s.handle(press(key))
	}
	if got := entryNames(s.picks()); !slices.Equal(got, []string{"asana", "github"}) {
		t.Errorf("picks = %v, want asana and github (ticked under Popular), once each", got)
	}
}

func TestCatalogScreen_filtersByDescription(t *testing.T) {
	c := testCatalog()
	s := loadedScreen(c, noImports)
	for _, key := range []string{"/", "p", "u", "l", "l"} {
		s.handle(press(key))
	}
	if text := catalogText(s); strings.Count(text, "[ ] github") != 1 || strings.Contains(text, "linear") {
		t.Errorf("screen:\n%s\nwant only github, once, matched by its description", text)
	}
	if line, keys := ansi.Strip(
		s.filterLine(),
	), s.keys(); line != "/pull_" ||
		!strings.HasPrefix(keys, "type to filter") {
		t.Errorf("filter line %q, keys %q; want the typed filter and how to end it, for the footer", line, keys)
	}
}

func TestCatalogScreen_aServerTickedOnImportIsHiddenAndLosesItsCatalogTick(t *testing.T) {
	c := testCatalog()
	var imported []config.ServerConfig
	s := loadedScreen(c, func() []config.ServerConfig { return imported })
	s.handle(press("space"))

	imported = []config.ServerConfig{{Name: "gh", URL: "https://api.github.example/mcp/"}}
	s.enter()

	if text := catalogText(s); strings.Contains(text, "github") {
		t.Errorf("screen:\n%s\nwant github hidden: the imported gh is the same URL", text)
	}
	imported = nil
	s.enter()
	if picks := s.picks(); len(picks) != 0 {
		t.Errorf("picks = %v; want github's catalog tick dropped once the Import row won", entryNames(picks))
	}
}

func TestCatalogScreen_loading(t *testing.T) {
	c := testCatalog()
	t.Run("shows loading until the catalog arrives, and isn't skipped meanwhile", func(t *testing.T) {
		s := newCatalogScreen(catalogParams{load: fromCatalog(c), offered: offerAll, imports: noImports})
		if text := catalogText(s); text != "loading…" || s.empty() {
			t.Errorf("before loading: screen %q, empty = %v; want loading… and not empty", text, s.empty())
		}
		s.update(s.start()())
		if text := catalogText(s); !strings.Contains(text, "[ ] linear") {
			t.Errorf("after loading:\n%s\nwant the catalog's servers", text)
		}
		if s.start() != nil {
			t.Error("start after loading returned a command; want no second fetch")
		}
	})
	t.Run("keys wait for the catalog, except going back", func(t *testing.T) {
		s := newCatalogScreen(catalogParams{load: fromCatalog(c), offered: offerAll, imports: noImports})
		for key, want := range map[string]step{"enter": stay, "/": stay, "space": stay, "esc": back} {
			if got := s.handle(press(key)); got != want {
				t.Errorf("%s while loading = %v, want %v", key, got, want)
			}
		}
		if s.list.filtering {
			t.Error("/ while loading started a filter the arriving catalog would discard")
		}
	})
	t.Run("the built-in catalog comes with a note saying why", func(t *testing.T) {
		load := func() (LoadedCatalog, error) {
			return LoadedCatalog{Catalog: c, Unavailable: errors.New("status 503")}, nil
		}
		s := newCatalogScreen(catalogParams{load: load, offered: offerAll, imports: noImports})
		s.update(s.start()())
		if text := catalogText(s); !strings.HasPrefix(
			text, "Showing the built-in catalog: the published one is unavailable (status 503)\n\n",
		) || !strings.Contains(text, "[ ] linear") {
			t.Errorf("screen:\n%s\nwant the note above the servers", text)
		}
	})
	t.Run("a catalog that can't be loaded says so", func(t *testing.T) {
		load := func() (LoadedCatalog, error) { return LoadedCatalog{}, errors.New("bad document") }
		s := newCatalogScreen(catalogParams{load: load, offered: offerAll, imports: noImports})
		s.update(s.start()())
		if text := catalogText(s); text != "The catalog couldn't be loaded: bad document" || s.empty() {
			t.Errorf("screen %q, empty = %v; want the error shown", text, s.empty())
		}
	})
	t.Run("is empty once every server is configured or imported", func(t *testing.T) {
		imported := func() []config.ServerConfig { return []config.ServerConfig{{Name: "linear"}, {Name: "sentry"}} }
		offered := func([]catalog.Entry) []catalog.Entry { return c.Entries[:2] }
		s := newCatalogScreen(catalogParams{load: fromCatalog(c), offered: offered, imports: imported})
		s.update(s.start()())
		if !s.empty() {
			t.Errorf(
				"screen:\n%s\nwant it empty: mini lacks only linear and sentry, and both are imported",
				catalogText(s),
			)
		}
	})
}
