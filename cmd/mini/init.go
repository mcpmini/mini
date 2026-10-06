package main

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/clock"
)

type initFlags struct {
	yes      bool
	from     string
	add      []string
	addGiven bool
}

const initLong = `Set up mini in three steps:
  1. import MCP servers from Claude Code, Claude Desktop, Cursor, and other clients,
  2. pick more from the server catalog,
  3. log in to the servers that use OAuth.
Servers that are already configured are never changed.`

const initExample = `  mini init
  mini init --from cursor
  mini init --yes --add linear,sentry`

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
			requested, err := requestedCatalogEntries(f)
			if err != nil {
				return err
			}
			runInit(opts.configDir, f, requested)
			return nil
		},
	}
	addInitFlags(cmd, &f)
	return cmd
}

func addInitFlags(cmd *cobra.Command, f *initFlags) {
	cmd.Flags().BoolVar(&f.yes, "yes", false, "run without prompts: import every detected client, skip the catalog picker, and leave logins for later")
	cmd.Flags().StringVar(&f.from, "from", "", "import only from this client ("+strings.Join(slices.Sorted(maps.Keys(fromClientNames)), ", ")+") or config file")
	cmd.Flags().StringSliceVar(&f.add, "add", nil, "catalog servers to add without the picker (comma-separated names)")
}

func runInit(configDir string, f initFlags, requested []catalog.Entry) {
	p := prompter{in: bufio.NewScanner(os.Stdin), out: os.Stderr}
	if err := createConfigDirs(configDir); err != nil {
		fatalf("create config dirs: %v", err)
	}
	fmt.Printf("config directory: %s\n", configDir)
	imported := importServers(configDir, f.from, importConfirmer(p, f.yes))
	detectImportedOAuth(oauthDetectParams{configDir: configDir, names: imported, clock: clock.System(), errOut: os.Stderr})
	runInitCatalogSelection(catalogStepParams{configDir: configDir, autoYes: f.yes, ask: p.ask, requested: requested})
	runLoginStep(newLoginStepParams(configDir, f.yes, p))
	printInstallInstructions()
}

func newLoginStepParams(configDir string, autoYes bool, p prompter) loginStepParams {
	return loginStepParams{
		configDir: configDir,
		autoYes:   autoYes,
		confirm:   p.confirm,
		ask:       p.ask,
		logIn:     logIn,
		out:       os.Stdout,
		errOut:    os.Stderr,
	}
}

func runInitCatalogSelection(p catalogStepParams) {
	p.loadCatalog, p.out, p.errOut = publishedCatalogSource().entries, os.Stdout, os.Stderr
	if err := runCatalogStep(p); err != nil {
		fatalf("catalog: %v", err)
	}
}

func importServers(configDir, from string, prompt func(string) bool) []string {
	if from != "" {
		return importFrom(configDir, from, prompt)
	}
	return importDetected(configDir, prompt)
}

func importDetected(configDir string, prompt func(string) bool) []string {
	detected := agents.Detect()
	if len(detected) == 0 {
		fmt.Println("no agent configs detected")
		return nil
	}
	var names []string
	for _, a := range detected {
		names = append(names, importAgentIfConfirmed(configDir, a, prompt)...)
	}
	return names
}

func importAgentIfConfirmed(configDir string, a agents.Agent, prompt func(string) bool) []string {
	q := fmt.Sprintf("import MCP servers from %s (%s)?", a.Name, a.ConfigPath)
	if !prompt(q) {
		return nil
	}
	names := importAgentConfig(configDir, a.Name, a)
	fmt.Printf("  imported %d server(s) from %s\n", len(names), a.Name)
	return names
}

func importFrom(configDir, from string, prompt func(string) bool) []string {
	source := resolveFromSource(from)
	if _, err := os.Stat(source.ConfigPath); err != nil {
		fatalf("config not found: %s", source.ConfigPath)
	}
	q := fmt.Sprintf("import MCP servers from %s?", source.ConfigPath)
	if !prompt(q) {
		return nil
	}
	names := importAgentConfig(configDir, source.ConfigPath, source)
	fmt.Printf("imported %d server(s) from %s\n", len(names), source.ConfigPath)
	return names
}

var fromClientNames = map[string]string{
	"claude-code":    "Claude Code",
	"claude-desktop": "Claude Desktop",
	"cursor":         "Cursor",
	"windsurf":       "Windsurf",
	"codex":          "Codex",
}

func resolveFromSource(from string) agents.Agent {
	if name, ok := fromClientNames[strings.ToLower(from)]; ok {
		agent, found := findKnownAgent(name)
		if !found {
			fatalf("could not find config for %q", from)
		}
		return agent
	}
	if strings.EqualFold(filepath.Ext(from), ".toml") {
		return agents.Agent{ConfigPath: from, Read: agents.ReadCodex}
	}
	return agents.Agent{ConfigPath: from, Read: agents.ReadClaude}
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
		if err := os.MkdirAll(filepath.Join(configDir, sub), 0700); err != nil {
			return err
		}
	}
	return nil
}

func resolveInstallBinPath() string {
	binPath, _ := os.Executable()
	if binPath == "" {
		return "/usr/local/bin/mini"
	}
	return binPath
}

func printInstallInstructions() {
	binPath := resolveInstallBinPath()
	fmt.Println("\nTo connect mini to your agent:")
	detected := agents.Detect()
	if len(detected) == 0 {
		fmt.Println()
		fmt.Println("  Add to your agent's MCP config:")
		fmt.Println(indent(renderMinimcpInstallJSON(binPath), "    "))
		return
	}
	for _, a := range detected {
		printAgentInstall(a, binPath)
	}
}

func shellQuoted(arg string) string {
	if !strings.ContainsAny(arg, " \t\n'\"\\$`;&|<>()*?[]{}!#~") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func printAgentInstall(a agents.Agent, binPath string) {
	fmt.Println()
	switch a.Name {
	case "Claude Code":
		fmt.Println("  Claude Code:")
		fmt.Println("    claude mcp add mini " + shellQuoted(binPath) + " connect")
		return
	case "Codex":
		fmt.Println("  Codex:")
		fmt.Println("    codex mcp add mini -- " + shellQuoted(binPath) + " connect")
		return
	}
	fmt.Printf("  %s — add to %s:\n", a.Name, a.ConfigPath)
	fmt.Println(indent(renderMinimcpInstallJSON(binPath), "    "))
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

type prompter struct {
	in  *bufio.Scanner
	out io.Writer
}

func (p prompter) ask(question string) string {
	fmt.Fprintf(p.out, "%s: ", question)
	if !p.in.Scan() {
		return ""
	}
	return strings.TrimSpace(p.in.Text())
}

func (p prompter) confirm(question string) bool {
	answer := strings.ToLower(p.ask(question + " [y/N]"))
	return answer == "y" || answer == "yes"
}

func importConfirmer(p prompter, autoYes bool) func(string) bool {
	if autoYes {
		return autoConfirm
	}
	return p.confirm
}

func autoConfirm(question string) bool {
	fmt.Println(question + " [auto: yes]")
	return true
}
