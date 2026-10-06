// Package initcmd holds mini init's logic: what to offer, what to write, and what happened.
// It returns data and never prints; the command presents it.
package initcmd

import (
	"net/url"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

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

// AvailableEntries are the catalog servers not configured yet, by name or URL, in catalog order.
func AvailableEntries(entries []catalog.Entry, servers []config.ServerConfig) []catalog.Entry {
	return slices.DeleteFunc(slices.Clone(entries), NewConfiguredKeys(servers).Has)
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
