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
	confirm   func(string) (bool, error)
	ask       func(string) (string, error)
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

func runLoginStep(p loginStepParams) error {
	cfg, err := config.LoadMain(p.configDir)
	if err != nil {
		printNotice(p.errOut, "skipping OAuth login: %v\n", err)
		return nil
	}
	servers, err := config.LoadServers(p.configDir)
	if err != nil {
		printNotice(p.errOut, "skipping OAuth login: %v\n", err)
		return nil
	}
	warnServerProblems(p.errOut, servers)
	candidates := findLoginCandidates(p.configDir, servers.Loaded)
	if len(candidates) == 0 {
		return nil
	}
	if err := printLoginCandidates(p.out, candidates); err != nil {
		return err
	}
	chosen, err := chooseLoginCandidates(p, candidates)
	if err != nil {
		return err
	}
	loggedIn := logInCandidates(p, cfg, chosen)
	printLoginReminders(p.out, notLoggedIn(candidates, loggedIn))
	return nil
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

func printLoginCandidates(out io.Writer, candidates []loginCandidate) error {
	if _, err := fmt.Fprintln(out, "OAuth login needed:"); err != nil {
		return err
	}
	for _, c := range candidates {
		if _, err := fmt.Fprintf(out, "  %s (%s)\n", c.server.Name, c.reason()); err != nil {
			return err
		}
	}
	return nil
}

func chooseLoginCandidates(p loginStepParams, candidates []loginCandidate) ([]loginCandidate, error) {
	answer, err := p.ask("Log in now? [a]ll / [p]ick / [s]kip")
	if err != nil {
		return nil, err
	}
	choice := strings.ToLower(strings.TrimSpace(answer))
	if choice == "a" || choice == "all" {
		return candidates, nil
	}
	if choice != "p" && choice != "pick" {
		return nil, nil
	}
	return pickLoginCandidates(p.confirm, candidates)
}

func pickLoginCandidates(confirm func(string) (bool, error), candidates []loginCandidate) ([]loginCandidate, error) {
	var chosen []loginCandidate
	for _, c := range candidates {
		confirmed, err := confirm("Log in to " + c.server.Name + "?")
		if err != nil {
			return nil, err
		}
		if confirmed {
			chosen = append(chosen, c)
		}
	}
	return chosen, nil
}

func logInCandidates(p loginStepParams, cfg *config.Config, candidates []loginCandidate) []string {
	var loggedIn []string
	for _, c := range candidates {
		if _, err := p.logIn(logInParams{configDir: p.configDir, cfg: cfg, sc: &c.server, out: p.out}); err != nil {
			printNotice(p.errOut, "login failed for %s: %v\n", c.server.Name, err)
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
	printNotice(out, "Log in later with:\n")
	for _, name := range names {
		printNotice(out, "  mini auth %s\n", name)
	}
}
