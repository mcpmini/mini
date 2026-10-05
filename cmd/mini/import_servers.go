package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

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

func importAgentConfig(configDir, source string, agent agents.Agent) []string {
	imp := serverImport{configDir: configDir, source: source, out: os.Stdout, errOut: os.Stderr}
	servers, err := agent.Read(agent.ConfigPath)
	if err != nil {
		fmt.Fprintf(imp.errOut, "  warning: %v\n", err)
		return nil
	}
	// Each failure was already warned about; init carries on either way.
	added, _ := imp.addAll(servers)
	return added
}

func (imp serverImport) addAll(servers map[string]agents.Server) (added []string, failed int) {
	selfPath, _ := os.Executable() //nolint:errcheck // without it, mini's own entry is imported like any other server
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		server := servers[name]
		if isSelfEntry(server.Config.Command, selfPath) {
			continue
		}
		if reason := notImportedReason(server); reason != "" {
			fmt.Fprintf(imp.out, "  %s: %s %s\n", imp.source, name, reason)
			continue
		}
		switch err := imp.add(server.Config); {
		case err == nil:
			imp.reportIgnored(name, server.IgnoredRunSettings)
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

func (imp serverImport) reportIgnored(name string, ignored []string) {
	if len(ignored) > 0 {
		fmt.Fprintf(imp.out, "  %s: %s imported without its %s, which mini doesn't support yet; if it fails to start, edit %s\n",
			imp.source, name, strings.Join(ignored, ", "), config.ServerPath(imp.configDir, name))
	}
}

func (imp serverImport) add(sc config.ServerConfig) error {
	added, err := ops.AddServer(imp.configDir, sc)
	if errors.Is(err, ops.ErrAlreadyConfigured) {
		imp.reportConfigured(sc)
		return err
	}
	if err != nil {
		fmt.Fprintf(imp.errOut, "  warning: %v\n", err)
		return err
	}
	printAdded(imp.out, added)
	return nil
}

func printAdded(w io.Writer, added ops.AddedServer) {
	fmt.Fprintf(w, "added %s → %s\n", added.Config.Name, added.Path)
	if added.ProjectionPath != "" {
		fmt.Fprintf(w, "installed default projection → %s\n", added.ProjectionPath)
	}
	if added.DefaultPermissions {
		fmt.Fprintf(w, "applied default permissions → %s\n", added.Path)
	}
}

// Names the differing fields but never their values: headers and env usually hold tokens.
func (imp serverImport) reportConfigured(imported config.ServerConfig) {
	path := config.ServerPath(imp.configDir, imported.Name)
	differences, err := configuredDifferences(path, imported)
	switch {
	case err != nil:
		fmt.Fprintf(imp.out, "  %s: %s not imported, %v\n", imp.source, imported.Name, err)
	case len(differences) == 0:
		fmt.Fprintf(imp.out, "  %s: %s already configured in mini\n", imp.source, imported.Name)
	default:
		fmt.Fprintf(imp.out, "  %s: %s not imported, mini's config has a different %s (edit %s to change it)\n",
			imp.source, imported.Name, strings.Join(differences, ", "), path)
	}
}

// Compares the file as written, before env expansion, since imported values are unexpanded too.
func configuredDifferences(path string, imported config.ServerConfig) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var configured config.ServerConfig
	// yaml errors can quote the offending value, which may be a header token.
	if yaml.Unmarshal(data, &configured) != nil {
		return nil, fmt.Errorf("could not compare it with %s, which does not parse", path)
	}
	return connectionDifferences(configured, imported), nil
}

func connectionDifferences(configured, imported config.ServerConfig) []string {
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
