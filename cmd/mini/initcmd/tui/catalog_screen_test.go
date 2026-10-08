package tui

import (
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
	text := catalogText(newCatalogScreen(c, c.Entries, noImports))
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
	s := newCatalogScreen(c, c.Entries, noImports)
	for _, key := range []string{"space", "down", "down", "space"} {
		s.handle(press(key))
	}
	if got := entryNames(s.picks()); !slices.Equal(got, []string{"asana", "github"}) {
		t.Errorf("picks = %v, want asana and github (ticked under Popular), once each", got)
	}
}

func TestCatalogScreen_filtersByDescription(t *testing.T) {
	c := testCatalog()
	s := newCatalogScreen(c, c.Entries, noImports)
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
	s := newCatalogScreen(c, c.Entries, func() []config.ServerConfig { return imported })
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
