package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

type catalogStepParams struct {
	configDir string
	autoYes   bool
	choose    func(string) string
	out       io.Writer
	err       io.Writer
}

type catalogSelectionParams struct {
	indexes *[]int
	seen    map[int]bool
	token   string
	count   int
}

func runCatalogStep(p catalogStepParams) error {
	if p.autoYes {
		return nil
	}
	entries, err := catalog.Load()
	if err != nil {
		return err
	}
	_, servers, err := config.Load(p.configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	available := availableCatalogEntries(entries, servers)
	if len(available) == 0 {
		return nil
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

func writeCatalogEntries(configDir string, entries []catalog.Entry, indexes []int) error {
	for _, index := range indexes {
		entry := entries[index]
		server := importers.ServerYAML{Name: entry.Name, Transport: "http", URL: entry.URL}
		if err := importers.WriteServerYAML(configDir, entry.Name, server); err != nil {
			return err
		}
	}
	return nil
}
