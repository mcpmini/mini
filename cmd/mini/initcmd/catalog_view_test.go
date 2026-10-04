package initcmd

import (
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

func entryNames(entries []catalog.Entry) []string {
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func TestAvailableCatalog(t *testing.T) {
	t.Run("filters configured names and URLs", func(t *testing.T) {
		c := catalog.Catalog{Popular: []string{"linear", "notion"}, Entries: []catalog.Entry{
			{Name: "github", URL: "https://github.example.com/mcp", Category: "Dev"},
			{Name: "linear", URL: "https://linear.example.com/mcp", Category: "Dev"},
			{Name: "notion", URL: "https://notion.example.com/mcp", Category: "Dev"},
		}}
		servers := []config.ServerConfig{{Name: "GitHub"}, {Name: "my-linear", URL: "https://LINEAR.example.com/mcp/"}}
		available := AvailableCatalog(c, servers)
		if names := entryNames(available.Entries); !reflect.DeepEqual(names, []string{"notion"}) {
			t.Errorf("available = %v, want only notion", names)
		}
		if !reflect.DeepEqual(available.Popular, []string{"notion"}) {
			t.Errorf("popular = %v, want only notion", available.Popular)
		}
	})
	t.Run("keeps flat entry order after filtering", func(t *testing.T) {
		c := catalog.Catalog{Entries: []catalog.Entry{
			{Name: "c", Category: "Dev"}, {Name: "taken", Category: "Configured"},
			{Name: "b", Category: "Data"}, {Name: "a", Category: "Dev"},
		}}
		available := AvailableCatalog(c, []config.ServerConfig{{Name: "taken"}})
		if names := entryNames(available.Entries); !reflect.DeepEqual(names, []string{"c", "b", "a"}) {
			t.Errorf("available = %v, want [c b a]", names)
		}
	})
}

type sectionSummary struct {
	title   string
	popular bool
	servers []string
}

func summarizeSections(sections []CatalogSection) []sectionSummary {
	var out []sectionSummary
	for _, s := range sections {
		out = append(out, sectionSummary{s.Title, s.Popular, entryNames(s.Servers)})
	}
	return out
}

func TestCatalogView(t *testing.T) {
	c := catalog.Catalog{Popular: []string{"linear", "github"}, Entries: []catalog.Entry{
		{Name: "github", URL: "https://github.example.com/mcp", Category: "Dev"},
		{Name: "linear", Category: "Work"}, {Name: "sentry", Category: "Dev"}, {Name: "notion", Category: "Work"},
	}}
	t.Run("popular first, in popular order, and again in its category", func(t *testing.T) {
		got := summarizeSections(CatalogView(c, nil, nil))
		want := []sectionSummary{
			{"Popular", true, []string{"linear", "github"}},
			{"Dev", false, []string{"github", "sentry"}},
			{"Work", false, []string{"linear", "notion"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sections = %+v\nwant %+v", got, want)
		}
	})
	t.Run("configured and import-picked servers are hidden everywhere", func(t *testing.T) {
		configured := []config.ServerConfig{{Name: "linear"}}
		picked := []config.ServerConfig{{Name: "gh", URL: "https://github.example.com/mcp"}}
		got := summarizeSections(CatalogView(c, configured, picked))
		want := []sectionSummary{
			{"Dev", false, []string{"sentry"}},
			{"Work", false, []string{"notion"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sections = %+v\nwant %+v", got, want)
		}
	})
}

func TestCatalogView_keepsInputOrderUntouched(t *testing.T) {
	c := catalog.Catalog{Popular: []string{"second", "first"}, Entries: []catalog.Entry{
		{Name: "first", Category: "Zeta"}, {Name: "second", Category: "Alpha"}, {Name: "third", Category: "Zeta"},
	}}
	before := append([]catalog.Entry(nil), c.Entries...)
	sections := summarizeSections(CatalogView(c, nil, nil))
	want := []sectionSummary{{"Popular", true, []string{"second", "first"}}, {"Zeta", false, []string{"first", "third"}}, {"Alpha", false, []string{"second"}}}
	if !reflect.DeepEqual(sections, want) || !reflect.DeepEqual(c.Entries, before) {
		t.Fatalf("sections = %+v, entries = %+v; want %+v and unchanged entries", sections, c.Entries, want)
	}
}
