package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
)

const noTerminalHelp = `mini init asks questions, so it needs a terminal. Without one, say what to set up:
  mini init --import             import the servers of every agent found
  mini init --from AGENT|PATH    import the servers of one agent or config file
  mini init --add NAME,...       add servers from the catalog
`

var errInitIncomplete = errors.New("init didn't finish everything; see above")

func (f initFlags) unattended() bool {
	return f.importAll || f.from != "" || f.addGiven
}

func runUnattendedInit(configDir string, f initFlags) error {
	run, err := unattendedRun(configDir, f)
	if err != nil {
		return err
	}
	if err := createConfigDirs(configDir); err != nil {
		return fmt.Errorf("create config dirs: %w", err)
	}
	return printReport(initcmd.RunUnattended(run))
}

// Every flag is checked before anything is written.
func unattendedRun(configDir string, f initFlags) (initcmd.Setup, error) {
	if f.addGiven && len(nonBlankNames(f.add)) == 0 {
		return initcmd.Setup{}, errEmptyAdd
	}
	entries, err := flagCatalog(f)
	if err != nil {
		return initcmd.Setup{}, err
	}
	requested, err := requestedCatalogEntries(f, entries)
	if err != nil {
		return initcmd.Setup{}, err
	}
	sources, err := importSources(f)
	if err != nil {
		return initcmd.Setup{}, err
	}
	return initcmd.Setup{
		ConfigDir:       configDir,
		Import:          sources,
		Add:             requested,
		Catalog:         entries,
		AgentsToConnect: agentsToConnect(),
		SelfPath:        selfPath(),
	}, nil
}

func selfPath() string {
	path, _ := os.Executable() //nolint:errcheck // without it, mini's own entry is recognized by its command name alone
	return path
}

func agentsToConnect() []agents.Agent {
	home, _ := os.UserHomeDir() //nolint:errcheck // without a home there are no agents to show how to connect
	return initcmd.ConnectableAgents(knownAgentsIn(home))
}

func knownAgentsIn(home string) []agents.Agent {
	if home == "" {
		return nil
	}
	return agents.Known(home)
}

func flagCatalog(f initFlags) ([]catalog.Entry, error) {
	if f.addGiven {
		return publishedCatalogSource().entries()
	}
	// Without --add the catalog only says which servers need a token or an app, so no fetch.
	c, err := catalog.Load()
	return c.Entries, err
}

func importSources(f initFlags) ([]agents.Agent, error) {
	switch {
	case f.importAll:
		return agents.Detect(), nil
	case f.from == "":
		return nil, nil
	}
	source, err := resolveFromSource(f.from)
	if err != nil {
		return nil, err
	}
	// The user named this source, so it fails the run before anything is written.
	if _, err := source.Read(source.ConfigPath); err != nil {
		return nil, err
	}
	return []agents.Agent{source}, nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func printNoTerminalHelp(w io.Writer) {
	fmt.Fprint(w, noTerminalHelp)
}

var errInitQuit = errors.New("init quit; nothing was written")

func runFullScreenInit(configDir string) error {
	setup, err := unattendedRun(configDir, initFlags{importAll: true})
	if err != nil {
		return err
	}
	plan, quit, err := tui.Run(tui.Params{Setup: setup, LoadCatalog: publishedCatalogSource().load})
	switch {
	case err != nil:
		return err
	case quit:
		return &exitError{code: 1, err: errInitQuit}
	}
	if err := createConfigDirs(configDir); err != nil {
		return fmt.Errorf("create config dirs: %w", err)
	}
	return printReport(setup.Write(plan))
}

func printReport(report initcmd.Report) error {
	fmt.Print(initcmd.Summary(report))
	if report.Failed() {
		return &exitError{code: 1, err: errInitIncomplete}
	}
	return nil
}
