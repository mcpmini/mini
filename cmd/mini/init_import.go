package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/cmd/mini/importers"
)

type claudeImport struct {
	configDir string
	source    string
	selfPath  string
}

// source names where the servers come from in messages: the agent's name, or the --from path.
func importClaudeFormat(configDir, source, path string) int {
	data, err := importers.ReadConfigFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		return 0
	}
	selfPath, _ := os.Executable() //nolint:errcheck // without it, mini's own entry is imported like any other server
	imp := claudeImport{configDir: configDir, source: source, selfPath: selfPath}
	servers := importers.ExtractClaudeMCPServers(data)
	imported := 0
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		if imp.importEntry(name, servers[name]) {
			imported++
		}
	}
	return imported
}

func (imp claudeImport) importEntry(name string, entry importers.ClaudeMCPEntry) bool {
	if isSelfEntry(entry.Command, imp.selfPath) {
		return false
	}
	server := importers.ClaudeEntryToServer(name, entry)
	err := importers.CreateServerYAML(imp.configDir, name, server)
	if errors.Is(err, fs.ErrExist) {
		imp.reportConfigured(name, server)
		return false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		return false
	}
	return true
}

// Names the differing fields but never their values: headers and env usually hold tokens.
func (imp claudeImport) reportConfigured(name string, imported importers.ServerYAML) {
	path := filepath.Join(imp.configDir, "servers", name+".yaml")
	differences, err := configuredDifferences(path, imported)
	switch {
	case err != nil:
		fmt.Printf("  %s: %s not imported, %v\n", imp.source, name, err)
	case len(differences) == 0:
		fmt.Printf("  %s: %s already configured in mini\n", imp.source, name)
	default:
		fmt.Printf("  %s: %s not imported, mini's config has a different %s (edit %s to change it)\n",
			imp.source, name, strings.Join(differences, ", "), path)
	}
}

// Compares the file as written, before env expansion, since imported values are unexpanded too.
func configuredDifferences(path string, imported importers.ServerYAML) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var configured importers.ServerYAML
	// yaml errors can quote the offending value, which may be a header token.
	if yaml.Unmarshal(data, &configured) != nil {
		return nil, fmt.Errorf("could not compare it with %s, which does not parse", path)
	}
	return connectionDifferences(configured, imported), nil
}

func connectionDifferences(configured, imported importers.ServerYAML) []string {
	fields := []struct {
		name string
		same bool
	}{
		{"transport", transportOrStdio(configured.Transport) == transportOrStdio(imported.Transport)},
		{"url", configured.URL == imported.URL},
		{"command", configured.Command == imported.Command},
		{"args", slices.Equal(configured.Args, imported.Args)},
		{"env", slices.Equal(slices.Sorted(slices.Values(configured.Env)), slices.Sorted(slices.Values(imported.Env)))},
		{"headers", maps.Equal(configured.Headers, imported.Headers)},
	}
	var differences []string
	for _, field := range fields {
		if !field.same {
			differences = append(differences, field.name)
		}
	}
	return differences
}

func transportOrStdio(transport string) string {
	if transport == "" {
		return "stdio"
	}
	return transport
}
