package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

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
	printAuthReminders(p.out, authReminderServers(needs, chosen, failed))
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

func authReminderServers(servers []authPassServer, chosen []config.ServerConfig, failed []string) []string {
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

func interactiveStringPrompter(scanner *bufio.Scanner) func(string) string {
	return func(question string) string {
		fmt.Fprintf(os.Stderr, "%s: ", question)
		if !scanner.Scan() {
			return ""
		}
		return strings.TrimSpace(scanner.Text())
	}
}
