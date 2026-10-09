package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

type catalogStepParams struct {
	configDir   string
	loadCatalog func() ([]catalog.Entry, error)
	ask         func(string) (string, error)
	out         io.Writer
	errOut      io.Writer
}

type catalogSource struct {
	client *http.Client
	url    string
}

func publishedCatalogSource() catalogSource {
	return catalogSource{client: catalog.NewFetchClient(), url: catalog.PublishedURL}
}

func (s catalogSource) entries() ([]catalog.Entry, error) {
	c, err := s.load()
	return c.Entries, err
}

// Any fetch failure, a document this build can't read included, quietly falls back to the built-in catalog.
func (s catalogSource) load() (catalog.Catalog, error) {
	if c, err := catalog.Fetch(context.Background(), s.client, s.url); err == nil {
		return c, nil
	}
	return catalog.Load()
}

func runCatalogStep(p catalogStepParams) error {
	entries, err := p.loadCatalog()
	if err != nil {
		return err
	}
	servers, err := configuredServers(p.configDir)
	if err != nil {
		return err
	}
	available := availableCatalogEntries(entries, servers)
	if len(available) == 0 {
		return nil
	}
	if err := printCatalogEntries(p.out, available); err != nil {
		return err
	}
	return selectCatalogEntries(p, available)
}

func configuredServers(configDir string) ([]config.ServerConfig, error) {
	// Broken files are left out silently; the login step that runs next reports each one once.
	servers, err := config.LoadServers(configDir)
	return servers.Loaded, err
}

func availableCatalogEntries(entries []catalog.Entry, servers []config.ServerConfig) []catalog.Entry {
	return initcmd.GroupByCategory(initcmd.AvailableEntries(entries, servers))
}

func printCatalogEntries(out io.Writer, entries []catalog.Entry) error {
	if _, err := fmt.Fprintln(out, "Available MCP servers:"); err != nil {
		return err
	}
	category := ""
	for i, entry := range entries {
		if entry.Category != category {
			category = entry.Category
			if _, err := fmt.Fprintf(out, "  %s:\n", category); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(
			out,
			"    %d. %s [%s] - %s%s\n",
			i+1,
			entry.Title,
			entryHost(entry.URL),
			entry.Description,
			authLabel(entry.Auth),
		); err != nil {
			return err
		}
	}
	return nil
}

func authLabel(auth string) string {
	switch auth {
	case catalog.AuthOAuth2:
		return " (OAuth login)"
	case catalog.AuthOAuth2App:
		return " (OAuth, needs your own app)"
	case catalog.AuthToken:
		return " (needs an access token)"
	}
	return ""
}

func entryHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

func selectCatalogEntries(p catalogStepParams, entries []catalog.Entry) error {
	for {
		answer, err := p.ask("Select servers (numbers, ranges, a = all, empty = none)")
		if err != nil {
			return err
		}
		indexes, err := parseCatalogSelection(answer, len(entries))
		if err != nil {
			printNotice(p.errOut, "invalid selection: %v\n", err)
			continue
		}
		written, err := writeCatalogEntries(p, entries, indexes)
		printSetupNotes(p, entries, written)
		return err
	}
}

func printSetupNotes(p catalogStepParams, entries []catalog.Entry, indexes []int) {
	for _, index := range indexes {
		e := entries[index]
		status := initcmd.ServerStatus{Name: e.Name, SetupURL: e.SetupURL}
		switch e.Auth {
		case catalog.AuthToken:
			status.Readiness = initcmd.NeedsToken
		case catalog.AuthOAuth2App:
			status.Readiness = initcmd.NeedsOwnApp
		default:
			continue
		}
		printNotice(p.out, "%s %s\n", e.Name, initcmd.SetupStep(p.configDir, status))
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

func writeCatalogEntries(p catalogStepParams, entries []catalog.Entry, indexes []int) ([]int, error) {
	var written []int
	for _, index := range indexes {
		added, err := ops.AddServer(p.configDir, initcmd.CatalogServer(entries[index]))
		if errors.Is(err, ops.ErrAlreadyConfigured) {
			printNotice(p.out, "  %s already configured in mini\n", entries[index].Name)
			continue
		}
		if err != nil {
			return written, err
		}
		printAdded(p.out, added)
		written = append(written, index)
	}
	return written, nil
}
