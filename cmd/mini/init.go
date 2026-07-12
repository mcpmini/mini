package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

const importFileSizeLimit = 4 * 1024 * 1024 // 4MB — sane upper bound for any agent config file

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
	scanner := bufio.NewScanner(os.Stdin)
	prompt := interactivePrompter(scanner, f.yes)
	if err := createConfigDirs(configDir); err != nil {
		fatalf("create config dirs: %v", err)
	}
	fmt.Printf("config directory: %s\n", configDir)
	if imported := importServers(configDir, f.from, prompt); imported > 0 {
		fmt.Printf("imported %d server(s)\n", imported)
	}
	runInitAuthPass(initAuthPassParams{
		configDir: configDir,
		autoYes:   f.yes,
		confirm:   prompt,
		choose:    interactiveStringPrompter(scanner),
	})
	printInstallInstructions()
}

type initAuthPassParams struct {
	configDir string
	autoYes   bool
	confirm   func(string) bool
	choose    func(string) string
}

func runInitAuthPass(p initAuthPassParams) {
	if err := runAuthPass(authPassParams{
		configDir: p.configDir,
		autoYes:   p.autoYes,
		confirm:   p.confirm,
		choose:    p.choose,
		authorize: doPKCEFlow,
		out:       os.Stdout,
		err:       os.Stderr,
	}); err != nil {
		fatalf("auth setup: %v", err)
	}
}

type authPassParams struct {
	configDir string
	autoYes   bool
	confirm   func(string) bool
	choose    func(string) string
	authorize func(pkceFlowParams) (*oauth2.Token, error)
	out       io.Writer
	err       io.Writer
}

type authPassServer struct {
	config.ServerConfig
	note string
}

func runAuthPass(p authPassParams) error {
	cfg, servers, err := config.Load(p.configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	needs := findAuthPassServers(p.configDir, servers)
	if len(needs) == 0 {
		return nil
	}
	printAuthPassServers(p.out, needs)
	chosen := chooseAuthPassServers(p, needs)
	failed := authorizeAuthPassServers(p, cfg, chosen)
	printAuthReminders(p.out, unchosenAuthPassServers(needs, chosen, failed))
	return nil
}

func findAuthPassServers(configDir string, servers []config.ServerConfig) []authPassServer {
	var needs []authPassServer
	for _, sc := range servers {
		need := auth.NeedsAuthorization(configDir, sc)
		if need.Needed {
			needs = append(needs, authPassServer{ServerConfig: sc, note: need.Note})
		}
	}
	return needs
}

func printAuthPassServers(out io.Writer, servers []authPassServer) {
	fmt.Fprintln(out, "OAuth authorization needed:")
	for _, server := range servers {
		line := "  " + server.Name
		if server.note != "" {
			line += " (" + server.note + ")"
		}
		fmt.Fprintln(out, line)
	}
}

func chooseAuthPassServers(p authPassParams, servers []authPassServer) []config.ServerConfig {
	if p.autoYes {
		return nil
	}
	choice := strings.ToLower(strings.TrimSpace(p.choose("Authorize now? [a]ll / [p]ick / [s]kip")))
	if choice == "a" || choice == "all" {
		return authPassServerConfigs(servers)
	}
	if choice != "p" && choice != "pick" {
		return nil
	}
	return pickAuthPassServers(p.confirm, servers)
}

func authPassServerConfigs(servers []authPassServer) []config.ServerConfig {
	chosen := make([]config.ServerConfig, len(servers))
	for i := range servers {
		chosen[i] = servers[i].ServerConfig
	}
	return chosen
}

func pickAuthPassServers(confirm func(string) bool, servers []authPassServer) []config.ServerConfig {
	var chosen []config.ServerConfig
	for _, server := range servers {
		if confirm("Authorize " + server.Name + "?") {
			chosen = append(chosen, server.ServerConfig)
		}
	}
	return chosen
}

func authorizeAuthPassServers(p authPassParams, cfg *config.Config, servers []config.ServerConfig) []string {
	var failed []string
	for _, sc := range servers {
		token, err := p.authorize(pkceFlowParams{configDir: p.configDir, serverName: sc.Name, opener: authOpener(sc.Auth.BrowserCmd, cfg.BrowserCommand, cfg.DisableAuthBrowserOpen), sc: &sc})
		if err != nil {
			fmt.Fprintf(p.err, "authorization failed for %s: %v\n", sc.Name, err)
			failed = append(failed, sc.Name)
			continue
		}
		printAuthResultTo(p.out, sc.Name, token.Expiry)
	}
	return failed
}

func unchosenAuthPassServers(servers []authPassServer, chosen []config.ServerConfig, failed []string) []string {
	chosenNames := map[string]bool{}
	for _, sc := range chosen {
		chosenNames[sc.Name] = true
	}
	failedNames := map[string]bool{}
	for _, name := range failed {
		failedNames[name] = true
	}
	var reminders []string
	for _, server := range servers {
		if !chosenNames[server.Name] || failedNames[server.Name] {
			reminders = append(reminders, server.Name)
		}
	}
	return reminders
}

func printAuthReminders(out io.Writer, names []string) {
	if len(names) == 0 {
		return
	}
	fmt.Fprintln(out, "Authorize later with:")
	for _, name := range names {
		fmt.Fprintf(out, "  mini auth %s\n", name)
	}
}

func importServers(configDir, from string, prompt func(string) bool) int {
	if from != "" {
		return importFrom(configDir, from, prompt)
	}
	return importDetected(configDir, prompt)
}

func importDetected(configDir string, prompt func(string) bool) int {
	clients := detectAgentClients()
	if len(clients) == 0 {
		fmt.Println("no agent configs detected")
		return 0
	}
	total := 0
	for _, c := range clients {
		total += importClientIfConfirmed(configDir, c, prompt)
	}
	return total
}

func importClientIfConfirmed(configDir string, c agentClient, prompt func(string) bool) int {
	q := fmt.Sprintf("import MCP servers from %s (%s)?", c.Name, c.ConfigPath)
	if !prompt(q) {
		return 0
	}
	n := importClaudeFormat(configDir, c.ConfigPath)
	fmt.Printf("  imported %d server(s) from %s\n", n, c.Name)
	return n
}

func importFrom(configDir, from string, prompt func(string) bool) int {
	path := resolveFromPath(from)
	if _, err := os.Stat(path); err != nil {
		fatalf("config not found: %s", path)
	}
	q := fmt.Sprintf("import MCP servers from %s?", path)
	if !prompt(q) {
		return 0
	}
	n := importClaudeFormat(configDir, path)
	fmt.Printf("imported %d server(s) from %s\n", n, path)
	return n
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

func importClaudeFormat(configDir, path string) int {
	data, ok := readImportFile(path)
	if !ok {
		return 0
	}
	selfPath, _ := os.Executable()
	return importClaudeServers(configDir, data, selfPath)
}

func readImportFile(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		warnImportRead(path, err)
		return nil, false
	}
	defer f.Close() //nolint:errcheck

	data, err := io.ReadAll(io.LimitReader(f, importFileSizeLimit))
	if err != nil {
		warnImportRead(path, err)
		return nil, false
	}
	return data, true
}

func warnImportRead(path string, err error) {
	fmt.Fprintf(os.Stderr, "  warning: read %s: %v\n", path, err)
}

func importClaudeServers(configDir string, data []byte, selfPath string) int {
	imported := 0
	for name, entry := range importers.ExtractClaudeMCPServers(data) {
		if shouldImportClaudeEntry(configDir, name, entry, selfPath) {
			imported++
		}
	}
	return imported
}

func shouldImportClaudeEntry(configDir, name string, entry importers.ClaudeMCPEntry, selfPath string) bool {
	if isSelfEntry(entry.Command, selfPath) {
		return false
	}
	if err := importers.WriteServerYAML(configDir, name, importers.ClaudeEntryToServer(name, entry)); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		return false
	}
	return true
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

func interactivePrompter(scanner *bufio.Scanner, autoYes bool) func(string) bool {
	return func(question string) bool {
		if autoYes {
			fmt.Println(question + " [auto: yes]")
			return true
		}
		fmt.Fprintf(os.Stderr, "%s [y/N]: ", question)
		if !scanner.Scan() {
			return false
		}
		ans := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return ans == "y" || ans == "yes"
	}
}

func interactiveStringPrompter(scanner *bufio.Scanner) func(string) string {
	return func(question string) string {
		fmt.Fprintf(os.Stderr, "%s: ", question)
		if !scanner.Scan() {
			return ""
		}
		return strings.TrimSpace(scanner.Text())
	}
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
