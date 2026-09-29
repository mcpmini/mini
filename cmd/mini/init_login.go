package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type loginStepParams struct {
	configDir string
	autoYes   bool
	confirm   func(string) bool
	ask       func(string) string
	logIn     func(logInParams) (*oauth2.Token, error)
	out       io.Writer
	errOut    io.Writer
}

type loginCandidate struct {
	server  config.ServerConfig
	state   auth.TokenState
	readErr error
}

func (c loginCandidate) reason() string {
	if c.readErr != nil {
		return c.state.String() + ": " + c.readErr.Error()
	}
	return c.state.String()
}

func runLoginStep(p loginStepParams) {
	cfg, servers, err := config.Load(p.configDir)
	if err != nil {
		// Imports are already written; an env var this shell doesn't export must not abort init.
		fmt.Fprintf(p.errOut, "skipping OAuth login: load config: %v\n", err)
		return
	}
	candidates := findLoginCandidates(p.configDir, servers)
	if len(candidates) == 0 {
		return
	}
	printLoginCandidates(p.out, candidates)
	chosen := chooseLoginCandidates(p, candidates)
	loggedIn := logInCandidates(p, cfg, chosen)
	printLoginReminders(p.out, notLoggedIn(candidates, loggedIn))
}

func findLoginCandidates(configDir string, servers []config.ServerConfig) []loginCandidate {
	var candidates []loginCandidate
	for _, sc := range servers {
		if !sc.IsEnabled() || !sc.UsesOAuthLogin() {
			continue
		}
		state, readErr := auth.ReadTokenState(configDir, sc.Name)
		if state.NeedsLogin() {
			candidates = append(candidates, loginCandidate{server: sc, state: state, readErr: readErr})
		}
	}
	return candidates
}

func printLoginCandidates(out io.Writer, candidates []loginCandidate) {
	fmt.Fprintln(out, "OAuth login needed:")
	for _, c := range candidates {
		fmt.Fprintf(out, "  %s (%s)\n", c.server.Name, c.reason())
	}
}

func chooseLoginCandidates(p loginStepParams, candidates []loginCandidate) []loginCandidate {
	if p.autoYes {
		return nil
	}
	choice := strings.ToLower(strings.TrimSpace(p.ask("Log in now? [a]ll / [p]ick / [s]kip")))
	if choice == "a" || choice == "all" {
		return candidates
	}
	if choice != "p" && choice != "pick" {
		return nil
	}
	return pickLoginCandidates(p.confirm, candidates)
}

func pickLoginCandidates(confirm func(string) bool, candidates []loginCandidate) []loginCandidate {
	var chosen []loginCandidate
	for _, c := range candidates {
		if confirm("Log in to " + c.server.Name + "?") {
			chosen = append(chosen, c)
		}
	}
	return chosen
}

func logInCandidates(p loginStepParams, cfg *config.Config, candidates []loginCandidate) []string {
	var loggedIn []string
	for _, c := range candidates {
		if _, err := p.logIn(logInParams{configDir: p.configDir, cfg: cfg, sc: &c.server, out: p.out}); err != nil {
			fmt.Fprintf(p.errOut, "login failed for %s: %v\n", c.server.Name, err)
			continue
		}
		loggedIn = append(loggedIn, c.server.Name)
	}
	return loggedIn
}

func notLoggedIn(candidates []loginCandidate, loggedIn []string) []string {
	var pending []string
	for _, c := range candidates {
		if !slices.Contains(loggedIn, c.server.Name) {
			pending = append(pending, c.server.Name)
		}
	}
	return pending
}

func printLoginReminders(out io.Writer, names []string) {
	if len(names) == 0 {
		return
	}
	fmt.Fprintln(out, "Log in later with:")
	for _, name := range names {
		fmt.Fprintf(out, "  mini auth %s\n", name)
	}
}
