package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/catalog"
)

func resolveCatalogNames(entries []catalog.Entry, names []string) ([]catalog.Entry, error) {
	byName := catalogEntriesByName(entries)
	var resolved []catalog.Entry
	var unknown []string
	seen := make(map[string]bool)
	for _, name := range names {
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		if entry, ok := byName[key]; ok {
			resolved = append(resolved, entry)
		} else {
			unknown = append(unknown, name)
		}
	}
	if err := unknownCatalogNamesError(unknown); err != nil {
		return nil, err
	}
	return resolved, nil
}

func catalogEntriesByName(entries []catalog.Entry) map[string]catalog.Entry {
	byName := make(map[string]catalog.Entry, len(entries))
	for _, entry := range entries {
		byName[strings.ToLower(entry.Name)] = entry
	}
	return byName
}

func unknownCatalogNamesError(unknown []string) error {
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("not in the server catalog: %s", strings.Join(unknown, ", "))
}

func requestedCatalogEntries(f initFlags) ([]catalog.Entry, error) {
	if !f.addGiven {
		return nil, nil
	}
	names := nonBlankNames(f.add)
	if len(names) == 0 {
		return nil, errors.New("--add needs at least one server name")
	}
	c, err := publishedCatalogSource().load()
	if err != nil {
		return nil, err
	}
	return resolveCatalogNames(c.Entries(), names)
}

func addRequestedCatalogEntries(p catalogStepParams) error {
	servers, err := configuredServers(p.configDir)
	if err != nil {
		return err
	}
	configured := configuredKeys(servers)
	toWrite := make([]catalog.Entry, 0, len(p.requested))
	for _, entry := range p.requested {
		if isConfigured(configured, entry) {
			fmt.Fprintf(p.out, "  %s already configured in mini\n", entry.Name)
			continue
		}
		toWrite = append(toWrite, entry)
	}
	written, err := writeCatalogEntries(p, toWrite, allCatalogIndexes(len(toWrite)))
	printSetupNotes(p.out, toWrite, written)
	return err
}

func nonBlankNames(names []string) []string {
	var kept []string
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			kept = append(kept, name)
		}
	}
	return kept
}
