package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/spf13/cobra"
)

type initFlags struct {
	yes  bool
	from string
}

func newInitCmd(opts *rootOptions) *cobra.Command {
	f := initFlags{}
	cmd := &cobra.Command{
		Use:     "init",
		Aliases: []string{"setup"},
		Short:   "Interactive setup wizard",
		RunE: func(cmd *cobra.Command, args []string) error {
			runInit(opts.configDir, f)
			return nil
		},
	}
	cmd.Flags().BoolVar(&f.yes, "yes", false, "accept all prompts without interaction")
	cmd.Flags().StringVar(&f.from, "from", "", "import from specific client name or config path")
	return cmd
}

func runInit(configDir string, f initFlags) {
	p := prompter{in: bufio.NewScanner(os.Stdin), out: os.Stderr}
	if err := createConfigDirs(configDir); err != nil {
		fatalf("create config dirs: %v", err)
	}
	fmt.Printf("config directory: %s\n", configDir)
	imported := importServers(configDir, f.from, importConfirmer(p, f.yes))
	detectImportedOAuth(oauthDetectParams{configDir: configDir, names: imported, clock: clock.System(), errOut: os.Stderr})
	runInitCatalogSelection(configDir, f.yes, p.ask)
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

func runInitCatalogSelection(configDir string, autoYes bool, ask func(string) string) {
	if err := runCatalogStep(catalogStepParams{
		configDir: configDir,
		autoYes:   autoYes,
		ask:       ask,
		out:       os.Stdout,
		errOut:    os.Stderr,
	}); err != nil {
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
	clients := detectAgentClients()
	if len(clients) == 0 {
		fmt.Println("no agent configs detected")
		return nil
	}
	var names []string
	for _, c := range clients {
		names = append(names, importClientIfConfirmed(configDir, c, prompt)...)
	}
	return names
}

func importClientIfConfirmed(configDir string, c agentClient, prompt func(string) bool) []string {
	q := fmt.Sprintf("import MCP servers from %s (%s)?", c.Name, c.ConfigPath)
	if !prompt(q) {
		return nil
	}
	names := importClaudeFormat(configDir, c.Name, c.ConfigPath)
	fmt.Printf("  imported %d server(s) from %s\n", len(names), c.Name)
	return names
}

func importFrom(configDir, from string, prompt func(string) bool) []string {
	path := resolveFromPath(from)
	if _, err := os.Stat(path); err != nil {
		fatalf("config not found: %s", path)
	}
	q := fmt.Sprintf("import MCP servers from %s?", path)
	if !prompt(q) {
		return nil
	}
	names := importClaudeFormat(configDir, path, path)
	fmt.Printf("imported %d server(s) from %s\n", len(names), path)
	return names
}

func resolveFromPath(from string) string {
	knownAliases := map[string]string{
		"claude-code":    findClientPath("Claude Code"),
		"claude-desktop": findClientPath("Claude Desktop"),
		"cursor":         findClientPath("Cursor"),
		"windsurf":       findClientPath("Windsurf"),
		"gemini":         findClientPath("Gemini CLI"),
	}
	if aliasPath, ok := knownAliases[strings.ToLower(from)]; ok {
		if aliasPath == "" {
			fatalf("could not find config for %q", from)
		}
		return aliasPath
	}
	return from
}

func findClientPath(name string) string {
	home, _ := os.UserHomeDir()
	for _, c := range knownClients(home) {
		if c.Name == name {
			return c.ConfigPath
		}
	}
	return ""
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
	clients := detectAgentClients()
	if len(clients) == 0 {
		fmt.Println()
		fmt.Println("  Add to your agent's MCP config:")
		fmt.Println(indent(renderMinimcpInstallJSON(binPath), "    "))
		return
	}
	for _, c := range clients {
		printClientInstall(c, binPath)
	}
}

func printClientInstall(c agentClient, binPath string) {
	fmt.Println()
	if c.Name == "Claude Code" {
		fmt.Println("  Claude Code:")
		fmt.Println("    claude mcp add mini " + binPath + " connect")
		return
	}
	fmt.Printf("  %s — add to %s:\n", c.Name, c.ConfigPath)
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

// isSelfEntry returns true if cmd resolves to the same binary as selfPath,
// so init doesn't re-import mini itself when it's already in the agent's MCP
// config (handles bare names, symlinks, and alternate build paths).
func isSelfEntry(cmd, selfPath string) bool {
	if cmd == "" || selfPath == "" {
		return false
	}
	candidates := dedup([]string{
		resolveExe(selfPath),
		resolveExe(filepath.Base(selfPath)),
	})
	cmdResolved := resolveExe(cmd)
	for _, c := range candidates {
		if cmdResolved == c {
			return true
		}
	}
	return false
}

// resolveExe resolves a command (absolute path, relative path, or bare name)
// to its real path on disk, following symlinks. Returns p unchanged on error.
func resolveExe(p string) string {
	if !filepath.IsAbs(p) {
		if found, err := exec.LookPath(p); err == nil {
			p = found
		}
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	out := ss[:0]
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
