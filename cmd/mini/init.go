package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/clock"
)

type initFlags struct {
	importAll bool
	from      string
	add       []string
	addGiven  bool
}

const initLong = `Set up mini in three steps:
  1. import MCP servers from Claude Code, Codex, Cursor, and other agents,
  2. pick more from the server catalog,
  3. log in to the servers that use OAuth.
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
		Short:   "Set up mini: import servers, pick more from the catalog, log in",
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
	case os.Getenv("MINI_NEW_INIT") == "1":
		return runFullScreenInit(configDir)
	}
	return runInit(configDir)
}

func addInitFlags(cmd *cobra.Command, f *initFlags) {
	cmd.Flags().BoolVar(&f.importAll, "import", false, "import the servers of every agent found, without asking")
	cmd.Flags().
		StringVar(&f.from, "from", "", "import the servers of one agent ("+strings.Join(slices.Sorted(maps.Keys(fromClientNames)), ", ")+") or config file, without asking")
	cmd.Flags().StringSliceVar(&f.add, "add", nil, "catalog servers to add, without asking (comma-separated names)")
}

func runInit(configDir string) error {
	p := prompter{in: bufio.NewScanner(os.Stdin), out: os.Stderr}
	if err := createConfigDirs(configDir); err != nil {
		return fmt.Errorf("create config dirs: %w", err)
	}
	fmt.Printf("config directory: %s\n", configDir)
	imported, err := importDetected(configDir, p.confirm)
	if err != nil {
		return err
	}
	detectImportedOAuth(
		oauthDetectParams{configDir: configDir, names: imported, clock: clock.System(), errOut: os.Stderr},
	)
	if err := runInitCatalogSelection(catalogStepParams{configDir: configDir, ask: p.ask}); err != nil {
		return err
	}
	if err := runLoginStep(newLoginStepParams(configDir, p)); err != nil {
		return err
	}
	printHandConnectSteps(configDir)
	return nil
}

func newLoginStepParams(configDir string, p prompter) loginStepParams {
	return loginStepParams{
		configDir: configDir,
		confirm:   p.confirm,
		ask:       p.ask,
		logIn:     logIn,
		out:       os.Stdout,
		errOut:    os.Stderr,
	}
}

func runInitCatalogSelection(p catalogStepParams) error {
	p.loadCatalog, p.out, p.errOut = publishedCatalogSource().entries, os.Stdout, os.Stderr
	return runCatalogStep(p)
}

func importDetected(configDir string, prompt func(string) (bool, error)) ([]string, error) {
	detected := agents.Detect()
	if len(detected) == 0 {
		fmt.Println("no agent configs detected")
		return nil, nil
	}
	var names []string
	for _, a := range detected {
		imported, err := importAgentIfConfirmed(configDir, a, prompt)
		if err != nil {
			return names, err
		}
		names = append(names, imported...)
	}
	return names, nil
}

func importAgentIfConfirmed(configDir string, a agents.Agent, prompt func(string) (bool, error)) ([]string, error) {
	q := fmt.Sprintf("import MCP servers from %s (%s)?", a.Name, a.ConfigPath)
	ok, err := prompt(q)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	names := importAgentConfig(configDir, a.Name, a)
	fmt.Printf("  imported %d server(s) from %s\n", len(names), a.Name)
	return names, nil
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

func printHandConnectSteps(configDir string) {
	fmt.Print(initcmd.HandConnectSteps(configDir, selfPath(), agentsToConnect()))
}

type prompter struct {
	in  *bufio.Scanner
	out io.Writer
}

func (p prompter) ask(question string) (string, error) {
	if _, err := fmt.Fprintf(p.out, "%s: ", question); err != nil {
		return "", err
	}
	if !p.in.Scan() {
		return "", p.in.Err()
	}
	return strings.TrimSpace(p.in.Text()), nil
}

func (p prompter) confirm(question string) (bool, error) {
	answer, err := p.ask(question + " [y/N]")
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(answer)
	return answer == "y" || answer == "yes", nil
}
