package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/defaults"
)

type catalogStepParams struct {
	configDir string
	autoYes   bool
	choose    func(string) string
	out       io.Writer
	err       io.Writer
	// nil falls back to the embedded catalog.
	resolve func() ([]catalog.Entry, error)
}

type catalogSelectionParams struct {
	indexes *[]int
	seen    map[int]bool
	token   string
	count   int
}

func runCatalogStep(p catalogStepParams) ([]catalog.Entry, error) {
	if p.autoYes {
		return nil, nil
	}
	entries, err := loadCatalogEntries(p.resolve)
	if err != nil {
		return nil, err
	}
	_, servers, err := config.Load(p.configDir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	available := availableCatalogEntries(entries, servers)
	if len(available) == 0 {
		return nil, nil
	}
	printCatalogEntries(p.out, available)
	return selectCatalogEntries(p, available)
}

func availableCatalogEntries(entries []catalog.Entry, servers []config.ServerConfig) []catalog.Entry {
	configured := make(map[string]bool, len(servers))
	for _, server := range servers {
		configured[server.Name] = true
	}
	var available []catalog.Entry
	for _, entry := range entries {
		if !configured[entry.Name] {
			available = append(available, entry)
		}
	}
	return available
}

func authSuffix(auth string) string {
	switch auth {
	case catalog.AuthToken:
		return " (token)"
	case catalog.AuthOAuth2:
		return " (oauth)"
	case catalog.AuthOAuth2App:
		return " (oauth · own app)"
	}
	return ""
}

func printCatalogEntries(out io.Writer, entries []catalog.Entry) {
	fmt.Fprintln(out, "Available MCP servers:")
	category := ""
	for i, entry := range entries {
		if entry.Category != category {
			category = entry.Category
			fmt.Fprintf(out, "  %s:\n", category)
		}
		fmt.Fprintf(out, "    %d. %s - %s%s\n", i+1, entry.Name, entry.Description, authSuffix(entry.Auth))
	}
}

func selectCatalogEntries(p catalogStepParams, entries []catalog.Entry) ([]catalog.Entry, error) {
	for {
		indexes, err := parseCatalogSelection(p.choose("Select servers (numbers, ranges, a = all, empty = none)"), len(entries))
		if err != nil {
			fmt.Fprintln(p.err, "invalid selection:", err)
			continue
		}
		return writeCatalogEntries(p.configDir, entries, indexes)
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
	seen := make(map[int]bool)
	var indexes []int
	for _, token := range strings.Split(input, ",") {
		if err := addCatalogSelection(catalogSelectionParams{
			indexes: &indexes,
			seen:    seen,
			token:   strings.TrimSpace(token),
			count:   count,
		}); err != nil {
			return nil, err
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

func addCatalogSelection(p catalogSelectionParams) error {
	start, end, err := catalogSelectionRange(p.token)
	if err != nil || start < 1 || end < start || end > p.count {
		return fmt.Errorf("%q is not a valid selection", p.token)
	}
	for i := start - 1; i < end; i++ {
		if !p.seen[i] {
			p.seen[i] = true
			*p.indexes = append(*p.indexes, i)
		}
	}
	return nil
}

func catalogSelectionRange(token string) (int, int, error) {
	if strings.Count(token, "-") == 0 {
		n, err := strconv.Atoi(token)
		return n, n, err
	}
	startText, endText, ok := strings.Cut(token, "-")
	if !ok || strings.Contains(endText, "-") {
		return 0, 0, fmt.Errorf("invalid range")
	}
	start, err := strconv.Atoi(startText)
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.Atoi(endText)
	return start, end, err
}

func loadCatalogEntries(resolve func() ([]catalog.Entry, error)) ([]catalog.Entry, error) {
	if resolve != nil {
		return resolve()
	}
	return catalog.Load()
}

func writeCatalogEntries(configDir string, entries []catalog.Entry, indexes []int) ([]catalog.Entry, error) {
	var guidance []catalog.Entry
	for _, index := range indexes {
		entry := entries[index]
		if err := importers.WriteServerYAML(configDir, entry.Name, catalogServerYAML(entry)); err != nil {
			return nil, err
		}
		if entry.NeedsManualSetup() {
			guidance = append(guidance, entry)
		}
	}
	return guidance, nil
}

func catalogServerYAML(entry catalog.Entry) importers.ServerYAML {
	s := importers.ServerYAML{Name: entry.Name, Transport: "http", URL: entry.URL}
	if entry.IsOAuth2() && !defaults.HasBundledAuth(entry.URL) {
		s.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	}
	return s
}

func printCatalogGuidance(out io.Writer, entries []catalog.Entry) {
	for _, e := range entries {
		switch e.Auth {
		case catalog.AuthToken:
			fmt.Fprintf(out, "%s needs an access token: create one at %s, then set auth: {type: bearer, token: $YOUR_TOKEN} in servers/%s.yaml\n", e.Name, e.SetupURL, e.Name)
		case catalog.AuthOAuth2App:
			fmt.Fprintf(out, "%s: create an OAuth app at %s, then add to servers/%s.yaml:\n  auth:\n    type: oauth2\n    client_id: <your app client id>\nthen run: mini auth %s\n", e.Name, e.SetupURL, e.Name, e.Name)
		}
	}
}
