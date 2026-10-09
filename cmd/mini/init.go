package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/agents"
)

type initFlags struct {
	importAll bool
	from      string
	add       []string
	addGiven  bool
}

const initLong = `Set up mini in four steps:
  1. import MCP servers from Claude Code, Codex, Cursor, and other agents,
  2. pick more from the server catalog,
  3. log in to the servers that use OAuth,
  4. connect mini to your agents, backing up each config it changes.
Servers that are already configured are never changed.

With --import, --from or --add, init asks nothing: it adds those servers, leaves logins and
agent configs alone, and prints what is left to do.`

const initExample = `  mini init
  mini init --import
  mini init --from cursor
  mini init --add linear,sentry`

func newInitCmd(opts *rootOptions) *cobra.Command {
	f := initFlags{}
	cmd := &cobra.Command{
		Use:     "init",
		Aliases: []string{"setup"},
		Short:   "Set up mini: import servers, pick more from the catalog, log in, connect your agents",
		Long:    initLong,
		Example: initExample,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.addGiven = cmd.Flags().Changed("add")
			return runInitCommand(opts.configDir, f)
		},
	}
	addInitFlags(cmd, &f)
	return cmd
}

func runInitCommand(configDir string, f initFlags) error {
	switch {
	case f.importAll && f.from != "":
		return usageErrf("--from and --import can't be used together: --import already reads every agent")
	case f.unattended():
		return runUnattendedInit(configDir, f)
	case !isTerminal(os.Stdin) || !isTerminal(os.Stdout):
		printNoTerminalHelp(os.Stderr)
		return &exitError{code: 1, err: errors.New("no terminal")}
	}
	return runFullScreenInit(configDir)
}

func addInitFlags(cmd *cobra.Command, f *initFlags) {
	cmd.Flags().BoolVar(&f.importAll, "import", false, "import the servers of every agent found, without asking")
	cmd.Flags().
		StringVar(&f.from, "from", "", "import the servers of one agent ("+strings.Join(slices.Sorted(maps.Keys(fromClientNames)), ", ")+") or config file, without asking")
	cmd.Flags().StringSliceVar(&f.add, "add", nil, "catalog servers to add, without asking (comma-separated names)")
}

var fromClientNames = map[string]string{
	"claude-code":    "Claude Code",
	"claude-desktop": "Claude Desktop",
	"cursor":         "Cursor",
	"windsurf":       "Windsurf",
	"codex":          "Codex",
}

func resolveFromSource(from string) (agents.Agent, error) {
	if name, ok := fromClientNames[strings.ToLower(from)]; ok {
		agent, found := findKnownAgent(name)
		if !found {
			return agents.Agent{}, fmt.Errorf("could not find config for %q", from)
		}
		return agent, nil
	}
	if strings.EqualFold(filepath.Ext(from), ".toml") {
		return agents.Agent{Name: from, ConfigPath: from, Read: agents.ReadCodex}, nil
	}
	return agents.Agent{Name: from, ConfigPath: from, Read: agents.ReadClaude}, nil
}

func findKnownAgent(name string) (agents.Agent, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return agents.Agent{}, false
	}
	for _, a := range agents.Known(home) {
		if a.Name == name && a.ConfigPath != "" {
			return a, true
		}
	}
	return agents.Agent{}, false
}

func createConfigDirs(configDir string) error {
	for _, sub := range []string{"servers", "internal", "internal/daemon", "internal/responses"} {
		if err := os.MkdirAll(filepath.Join(configDir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

const noTerminalHelp = `mini init asks questions, so it needs a terminal. Without one, say what to set up:
  mini init --import             import the servers of every agent found
  mini init --from AGENT|PATH    import the servers of one agent or config file
  mini init --add NAME,...       add servers from the catalog
`

func isTerminal(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}

func printNoTerminalHelp(w io.Writer) {
	printNotice(w, "%s", noTerminalHelp)
}
