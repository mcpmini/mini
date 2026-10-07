package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

type serverImport struct {
	configDir string
	source    string // the agent's name, or the config's path
	out       io.Writer
	errOut    io.Writer
}

func (imp serverImport) addAll(servers map[string]agents.Server) (added []string, failed int) {
	selfPath, _ := os.Executable() //nolint:errcheck // without it, mini's own entry is imported like any other server
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		server := servers[name]
		if agents.IsMiniEntry(server.Config, selfPath) {
			continue
		}
		if reason := notImportedReason(server); reason != "" {
			printNotice(imp.out, "  %s: %s %s\n", imp.source, name, reason)
			continue
		}
		switch err := imp.add(server.Config); {
		case err == nil:
			imp.reportCaveats(name, server)
			added = append(added, name)
		case !errors.Is(err, ops.ErrAlreadyConfigured):
			failed++
		}
	}
	return added, failed
}

func notImportedReason(server agents.Server) string {
	switch {
	case !server.Candidate():
		return "kept in the agent: uses " + strings.Join(server.UnexpandableRefs, ", ")
	case server.Disabled:
		// Importing a switched-off server would switch it on for every agent connected to mini.
		return "not imported: switched off in the agent"
	}
	return ""
}

func (imp serverImport) reportCaveats(name string, server agents.Server) {
	path := config.ServerPath(imp.configDir, name)
	if ignored := server.IgnoredRunSettings; len(ignored) > 0 {
		printNotice(imp.out, "  %s: %s\n", imp.source, initcmd.IgnoredSettingsNote(name, ignored, path))
	}
	for _, note := range initcmd.StaticHeaderNotes(name, server.UnusedEnvHeaders, path) {
		printNotice(imp.out, "  %s: %s\n", imp.source, note)
	}
	if err := config.UnsetEnvRefs(server.Config); err != nil {
		printNotice(imp.out, "  %s: %s imported, but %v; set it, or edit %s\n", imp.source, name, err, path)
	}
}

func (imp serverImport) add(sc config.ServerConfig) error {
	added, err := ops.AddServer(imp.configDir, sc)
	if errors.Is(err, ops.ErrAlreadyConfigured) {
		imp.reportConfigured(sc)
		return err
	}
	if err != nil {
		printNotice(imp.errOut, "  warning: %v\n", err)
		return err
	}
	printAdded(imp.out, added)
	return nil
}

func printAdded(w io.Writer, added ops.AddedServer) {
	if added.DefaultProjections {
		printNotice(w, "added %s → %s (with default projections)\n", added.Config.Name, added.Path)
	} else {
		printNotice(w, "added %s → %s\n", added.Config.Name, added.Path)
	}
	if added.DefaultPermissions {
		printNotice(w, "applied default permissions → %s\n", added.Path)
	}
}

// Names the differing fields but never their values: headers and env usually hold tokens.
func (imp serverImport) reportConfigured(imported config.ServerConfig) {
	path := config.ServerPath(imp.configDir, imported.Name)
	differences, err := configuredDifferences(imp.configDir, imported)
	switch {
	case err != nil:
		printNotice(imp.out, "  %s: %s not imported, %v\n", imp.source, imported.Name, err)
	case len(differences) == 0:
		printNotice(imp.out, "  %s: %s already configured in mini\n", imp.source, imported.Name)
	default:
		printNotice(imp.out, "  %s: %s not imported, mini's config has a different %s (edit %s to change it)\n",
			imp.source, imported.Name, strings.Join(differences, ", "), path)
	}
}

// Compares the file as written, before env expansion, since imported values are unexpanded too.
func configuredDifferences(configDir string, imported config.ServerConfig) ([]string, error) {
	configured, err := config.ReadUnexpandedServer(configDir, imported.Name)
	if err != nil {
		return nil, fmt.Errorf("could not compare it: %w", err)
	}
	return agents.ConnectionDifferences(configured, imported), nil
}
