package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/mcpmini/mini/internal/catalog"
)

func resolveCatalogNames(entries []catalog.Entry, names []string) ([]catalog.Entry, error) {
	byName := make(map[string]catalog.Entry, len(entries))
	for _, entry := range entries {
		byName[strings.ToLower(entry.Name)] = entry
	}
	var resolved []catalog.Entry
	var unknown []string
	seen := make(map[string]bool)
	for _, name := range names {
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		entry, ok := byName[key]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		resolved = append(resolved, entry)
	}
	if err := unknownCatalogNamesError(unknown); err != nil {
		return nil, err
	}
	return resolved, nil
}

func unknownCatalogNamesError(unknown []string) error {
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("not in the server catalog: %s", strings.Join(unknown, ", "))
}

func requestedCatalogEntries(names []string) ([]catalog.Entry, error) {
	if len(names) == 0 {
		return nil, nil
	}
	source := catalogSource{client: catalog.NewFetchClient(), url: catalog.PublishedURL, warn: os.Stderr}
	entries, err := source.entries()
	if err != nil {
		return nil, err
	}
	return resolveCatalogNames(entries, names)
}

func addRequestedCatalogEntries(p catalogStepParams) error {
	configured := configuredKeys(configuredServers(p.configDir))
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
