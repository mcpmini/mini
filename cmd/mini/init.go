package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/cmd/mini/importers"
)

const importFileSizeLimit = 4 * 1024 * 1024 // 4MB — sane upper bound for any agent config file

func runInit(configDir string, args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	yes := fs.Bool("yes", false, "accept all prompts without interaction")
	from := fs.String("from", "", "import from specific client name or config path")
	fs.Parse(args)
	scanner := bufio.NewScanner(os.Stdin)
	prompt := interactivePrompter(scanner, *yes)
	if err := createConfigDirs(configDir); err != nil {
		fatalf("create config dirs: %v", err)
	}
	fmt.Printf("config directory: %s\n", configDir)
	imported := importServers(configDir, *from, prompt)
	if imported > 0 {
		fmt.Printf("imported %d server(s)\n", imported)
	}
	printInstallInstructions()
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
		q := fmt.Sprintf("import MCP servers from %s (%s)?", c.Name, c.ConfigPath)
		if !prompt(q) {
			continue
		}
		n := importClaudeFormat(configDir, c.ConfigPath)
		fmt.Printf("  imported %d server(s) from %s\n", n, c.Name)
		total += n
	}
	return total
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
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: read %s: %v\n", path, err)
		return 0
	}
	defer f.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(f, importFileSizeLimit))
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: read %s: %v\n", path, err)
		return 0
	}
	servers := importers.ExtractClaudeMCPServers(data)
	for name, entry := range servers {
		if err := importers.WriteServerYAML(configDir, name, importers.ClaudeEntryToServer(name, entry)); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
		}
	}
	return len(servers)
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
	for _, sub := range []string{"servers", "projections", "actions", "responses", "tokens"} {
		if err := os.MkdirAll(filepath.Join(configDir, sub), 0700); err != nil {
			return err
		}
	}
	return nil
}

func printInstallInstructions() {
	binPath, _ := os.Executable()
	if binPath == "" {
		binPath = "/usr/local/bin/mini"
	}
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
		fmt.Println("    claude mcp add mini " + binPath)
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
