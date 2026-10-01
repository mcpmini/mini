package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

type catalogStepParams struct {
	configDir string
	autoYes   bool
	ask       func(string) string
	out       io.Writer
	errOut    io.Writer
}

func runCatalogStep(p catalogStepParams) error {
	if p.autoYes {
		return nil
	}
	entries, err := catalog.Load()
	if err != nil {
		return err
	}
	available := availableCatalogEntries(entries, configuredServers(p.configDir))
	if len(available) == 0 {
		return nil
	}
	printCatalogEntries(p.out, available)
	return selectCatalogEntries(p, available)
}

// Files that fail to load are left out without a warning: the login step that runs
// next loads the same files and reports each one once.
func configuredServers(configDir string) []config.ServerConfig {
	return slices.Collect(maps.Values(config.LoadServerSet(configDir).Servers))
}

func availableCatalogEntries(entries []catalog.Entry, servers []config.ServerConfig) []catalog.Entry {
	configured := make(map[string]bool, 2*len(servers))
	for _, server := range servers {
		// Case-insensitive filesystems (macOS, Windows) map GitHub.yaml and github.yaml to one file.
		configured[strings.ToLower(server.Name)] = true
		if server.URL != "" {
			configured[serverURLKey(server.URL)] = true
		}
	}
	available := slices.DeleteFunc(slices.Clone(entries), func(entry catalog.Entry) bool {
		return configured[strings.ToLower(entry.Name)] || configured[serverURLKey(entry.URL)]
	})
	return groupByCategory(available)
}

// URLs that differ only in scheme or host case, or a trailing slash, reach the same server.
func serverURLKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Scheme, u.Host, u.Path = strings.ToLower(u.Scheme), strings.ToLower(u.Host), strings.TrimSuffix(u.Path, "/")
	return u.String()
}

// Selection numbers follow display order, and headers print only when the category
// changes, so each category's entries must be contiguous.
func groupByCategory(entries []catalog.Entry) []catalog.Entry {
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

func printCatalogEntries(out io.Writer, entries []catalog.Entry) {
	fmt.Fprintln(out, "Available MCP servers:")
	category := ""
	for i, entry := range entries {
		if entry.Category != category {
			category = entry.Category
			fmt.Fprintf(out, "  %s:\n", category)
		}
		fmt.Fprintf(out, "    %d. %s - %s\n", i+1, entry.Name, entry.Description)
	}
}

func selectCatalogEntries(p catalogStepParams, entries []catalog.Entry) error {
	for {
		indexes, err := parseCatalogSelection(p.ask("Select servers (numbers, ranges, a = all, empty = none)"), len(entries))
		if err != nil {
			fmt.Fprintln(p.errOut, "invalid selection:", err)
			continue
		}
		return writeCatalogEntries(p, entries, indexes)
	}
}

func parseCatalogSelection(input string, count int) ([]int, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}
	if strings.EqualFold(input, "a") {
		return allCatalogIndexes(count), nil
	}
	var indexes []int
	for _, token := range strings.Split(input, ",") {
		start, end, err := catalogSelectionRange(strings.TrimSpace(token), count)
		if err != nil {
			return nil, err
		}
		for i := start - 1; i < end; i++ {
			if !slices.Contains(indexes, i) {
				indexes = append(indexes, i)
			}
		}
	}
	return indexes, nil
}

func allCatalogIndexes(count int) []int {
	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}
	return indexes
}

func catalogSelectionRange(token string, count int) (int, int, error) {
	start, end, err := parseSelectionRange(token)
	if err != nil || start < 1 || end < start || end > count {
		return 0, 0, fmt.Errorf("%q is not a valid selection", token)
	}
	return start, end, nil
}

func parseSelectionRange(token string) (int, int, error) {
	startText, endText, isRange := strings.Cut(token, "-")
	if !isRange {
		endText = startText
	}
	start, err := strconv.Atoi(startText)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.Atoi(endText)
	return start, end, err
}

// The picker only hides servers that loaded, so a file under another name: field, or one
// that failed to load, can still hold an entry's name; CreateServer never replaces it.
func writeCatalogEntries(p catalogStepParams, entries []catalog.Entry, indexes []int) error {
	for _, index := range indexes {
		err := ops.CreateServer(p.configDir, catalogServerConfig(entries[index]))
		if errors.Is(err, fs.ErrExist) {
			fmt.Fprintf(p.out, "  %s already configured in mini\n", entries[index].Name)
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func catalogServerConfig(entry catalog.Entry) config.ServerConfig {
	sc := config.ServerConfig{Name: entry.Name, Transport: "http", URL: entry.URL}
	// An explicit auth block would shadow a vendor's bundled registration (client ID, callback port).
	if entry.Auth == catalog.AuthOAuth2 && !sc.HasBundledAuth() {
		sc.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	}
	return sc
}
