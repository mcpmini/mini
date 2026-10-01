package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

type catalogStepParams struct {
	configDir   string
	autoYes     bool
	loadCatalog func() ([]catalog.Entry, error)
	ask         func(string) string
	out         io.Writer
	errOut      io.Writer
}

type catalogSource struct {
	client *http.Client
	url    string
	warn   io.Writer
}

func (s catalogSource) entries() ([]catalog.Entry, error) {
	entries, err := catalog.Fetch(context.Background(), s.client, s.url)
	if err == nil {
		return entries, nil
	}
	fmt.Fprintf(s.warn, "note: using the built-in server catalog (the published one is unavailable: %v)\n", err)
	return catalog.Load()
}

func runCatalogStep(p catalogStepParams) error {
	if p.autoYes {
		return nil
	}
	entries, err := p.loadCatalog()
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

func configuredServers(configDir string) []config.ServerConfig {
	// Broken files are left out silently; the login step that runs next reports each one once.
	return slices.Collect(maps.Values(config.LoadServerSet(configDir).Servers))
}

func availableCatalogEntries(entries []catalog.Entry, servers []config.ServerConfig) []catalog.Entry {
	configured := make(map[string]bool, 2*len(servers))
	for _, server := range servers {
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

func serverURLKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Scheme, u.Host, u.Path = strings.ToLower(u.Scheme), strings.ToLower(u.Host), strings.TrimSuffix(u.Path, "/")
	return u.String()
}

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
		fmt.Fprintf(out, "    %d. %s [%s] - %s%s\n", i+1, entry.Title, entryHost(entry.URL), entry.Description, authLabel(entry.Auth))
	}
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
		indexes, err := parseCatalogSelection(p.ask("Select servers (numbers, ranges, a = all, empty = none)"), len(entries))
		if err != nil {
			fmt.Fprintln(p.errOut, "invalid selection:", err)
			continue
		}
		written, err := writeCatalogEntries(p, entries, indexes)
		printSetupNotes(p.out, entries, written)
		return err
	}
}

const tokenSetupNote = `%s needs an access token: create one at %s, then add it to servers/%s.yaml, for example:
  headers:
    Authorization: Bearer ${%s}
`

const appSetupNote = `%s needs your own OAuth app: register one at %s with redirect URI %s, then add to servers/%s.yaml:
  auth:
    type: oauth2
    client_id: <your app's client ID>
and run: mini auth %s
`

func printSetupNotes(out io.Writer, entries []catalog.Entry, indexes []int) {
	for _, index := range indexes {
		switch e := entries[index]; e.Auth {
		case catalog.AuthToken:
			fmt.Fprintf(out, tokenSetupNote, e.Name, e.SetupURL, e.Name, tokenEnvVar(e.Name))
		case catalog.AuthOAuth2App:
			fmt.Fprintf(out, appSetupNote, e.Name, e.SetupURL, auth.ResolvedCallbackURI(nil), e.Name, e.Name)
		}
	}
}

func tokenEnvVar(serverName string) string {
	return strings.ToUpper(strings.ReplaceAll(serverName, "-", "_")) + "_TOKEN"
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
		err := ops.CreateServer(p.configDir, catalogServerConfig(entries[index]))
		if errors.Is(err, fs.ErrExist) {
			fmt.Fprintf(p.out, "  %s already configured in mini\n", entries[index].Name)
			continue
		}
		if err != nil {
			return written, err
		}
		written = append(written, index)
	}
	return written, nil
}

func catalogServerConfig(entry catalog.Entry) config.ServerConfig {
	sc := config.ServerConfig{Name: entry.Name, Transport: "http", URL: entry.URL}
	if entry.Auth == catalog.AuthOAuth2 && !sc.HasBundledAuth() {
		sc.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	}
	return sc
}
