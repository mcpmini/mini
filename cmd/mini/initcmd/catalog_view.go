package initcmd

import (
	"net/url"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

type CatalogSection struct {
	Title   string
	Popular bool
	Servers []catalog.Entry
}

func CatalogView(c catalog.Catalog, configured, picked []config.ServerConfig) []CatalogSection {
	available := AvailableCatalog(c, slices.Concat(configured, picked))
	var sections []CatalogSection
	if popular := popularEntries(available); len(popular) > 0 {
		sections = append(sections, CatalogSection{Title: "Popular", Popular: true, Servers: popular})
	}
	return append(sections, categorySections(available.Entries)...)
}

func categorySections(entries []catalog.Entry) []CatalogSection {
	var sections []CatalogSection
	for _, entry := range GroupByCategory(entries) {
		if len(sections) == 0 || sections[len(sections)-1].Title != entry.Category {
			sections = append(sections, CatalogSection{Title: entry.Category})
		}
		last := &sections[len(sections)-1]
		last.Servers = append(last.Servers, entry)
	}
	return sections
}

func GroupByCategory(entries []catalog.Entry) []catalog.Entry {
	firstSeen := make(map[string]int)
	for i, entry := range entries {
		if _, ok := firstSeen[entry.Category]; !ok {
			firstSeen[entry.Category] = i
		}
	}
	return slices.SortedStableFunc(slices.Values(entries), func(a, b catalog.Entry) int {
		return firstSeen[a.Category] - firstSeen[b.Category]
	})
}

func popularEntries(c catalog.Catalog) []catalog.Entry {
	entries := c.Entries
	var popular []catalog.Entry
	for _, name := range c.Popular {
		i := slices.IndexFunc(entries, func(entry catalog.Entry) bool { return entry.Name == name })
		popular = append(popular, entries[i])
	}
	return popular
}

func AvailableCatalog(c catalog.Catalog, servers []config.ServerConfig) catalog.Catalog {
	configured := NewConfiguredKeys(servers)
	available := catalog.Catalog{Entries: slices.DeleteFunc(slices.Clone(c.Entries), configured.Has)}
	available.Popular = availablePopular(c.Popular, available.Entries)
	return available
}

func availablePopular(popular []string, available []catalog.Entry) []string {
	return slices.DeleteFunc(slices.Clone(popular), func(name string) bool {
		return !slices.ContainsFunc(available, func(entry catalog.Entry) bool { return entry.Name == name })
	})
}

// ConfiguredKeys matches a catalog server to a configured one by name or by URL, so a server
// configured under another name isn't offered twice.
type ConfiguredKeys map[string]bool

func NewConfiguredKeys(servers []config.ServerConfig) ConfiguredKeys {
	keys := make(ConfiguredKeys, 2*len(servers))
	for _, server := range servers {
		keys[strings.ToLower(server.Name)] = true
		if server.URL != "" {
			keys[serverURLKey(server.URL)] = true
		}
	}
	return keys
}

func (k ConfiguredKeys) Has(entry catalog.Entry) bool {
	return k[strings.ToLower(entry.Name)] || k[serverURLKey(entry.URL)]
}

func serverURLKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Scheme, u.Host, u.Path = strings.ToLower(u.Scheme), strings.ToLower(u.Host), strings.TrimSuffix(u.Path, "/")
	return u.String()
}
