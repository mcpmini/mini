//go:build test

package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func continueAfter(params continueAfterParams) string {
	params.T.Helper()
	return continueLine(
		screenAfter(
			screenAfterParams{T: params.T, Setup: params.Setup, Load: fromCatalog(params.Catalog), Keys: params.Keys},
		),
	)
}

func screenAfter(params screenAfterParams) string {
	params.T.Helper()
	var view string
	_, err := Run(Params{Setup: params.Setup, LoadCatalog: params.Load, Program: func(m tea.Model) error {
		pressing(params.Keys...)(m)
		view = shown(m.(*app))
		return nil
	}})
	if err != nil {
		params.T.Fatal(err)
	}
	return view
}

func connectingSetup(configDir string, list ...agents.Agent) initcmd.Setup {
	setup := setupFor(configDir, list...)
	setup.AgentsToConnect = list
	return setup
}

func entryWithAuth(name string, auth catalog.AuthKind) catalog.Entry {
	return catalog.Entry{Name: name, URL: "https://" + name + ".example.com/mcp", Auth: auth}
}

func configWithAServerToFinish(t *testing.T) string {
	t.Helper()
	configDir := t.TempDir()
	configtest.WriteServer(
		t,
		configDir,
		config.ServerConfig{Name: "files", Command: "run", Env: []string{"ROOT=${MINI_TEST_UNSET_ROOT}"}},
	)
	return configDir
}

func TestContinueHints_importNamesTheCatalogOnlyWhenItHasServersToOffer(t *testing.T) {
	for name, tc := range map[string]struct {
		entries []catalog.Entry
		want    string
	}{
		"servers to offer": {[]catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}, "  Continue   browse popular MCPs"},
		"nothing to offer": {nil, "  Continue"},
	} {
		t.Run(name, func(t *testing.T) {
			setup := setupFor(t.TempDir(), stdioAgent("Codex", "files"))
			if line := continueAfter(
				continueAfterParams{T: t, Setup: setup, Catalog: catalog.Catalog{Entries: tc.entries}, Keys: nil},
			); line != tc.want {
				t.Errorf("Continue line on Import = %q, want %q", line, tc.want)
			}
		})
	}
}

func TestContinueHints_importNamesNoCatalogThatFailedToLoad(t *testing.T) {
	failing := func() (catalog.Catalog, error) { return catalog.Catalog{}, errors.New("offline") }
	view := screenAfter(
		screenAfterParams{T: t, Setup: setupFor(t.TempDir(), stdioAgent("Codex", "files")), Load: failing, Keys: nil},
	)
	if line := continueLine(view); line != "  Continue" {
		t.Errorf("Continue line on Import = %q, want no hint: Catalog has only its load error to show", line)
	}
}

func TestContinueHints_catalogNamesNothingItsSaveCouldChange(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("sentry")}}
	for name, setup := range map[string]initcmd.Setup{
		"an OAuth pick":                 connectingSetup(t.TempDir(), stdioAgent("Codex")),
		"a configured server to finish": connectingSetup(configWithAServerToFinish(t), stdioAgent("Codex")),
	} {
		t.Run(name, func(t *testing.T) {
			if line := continueAfter(
				continueAfterParams{T: t, Setup: setup, Catalog: c, Keys: []string{"space"}},
			); line != "  Continue" {
				t.Errorf(
					"Continue line on Catalog = %q, want no hint: Logins or Connect follows only after the save",
					line,
				)
			}
		})
	}
}

func TestContinueHints_loginsNamesConnectOnlyWhenContinueOpensIt(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	t.Run("with an agent to connect", func(t *testing.T) {
		setup := connectingSetup(configWithAServerToFinish(t), stdioAgent("Codex"))
		if line := continueAfter(
			continueAfterParams{T: t, Setup: setup, Catalog: c, Keys: []string{"tab", "enter"}},
		); line != "> Continue   add mini to your agents" {
			t.Errorf("Continue line on Logins = %q, want add mini to your agents", line)
		}
		view := screenAfter(
			screenAfterParams{T: t, Setup: setup, Load: fromCatalog(c), Keys: []string{"tab", "enter", "tab", "enter"}},
		)
		if !strings.Contains(view, "Connect mini to Codex") {
			t.Errorf("after Continue on Logins:\n%s\nwant Connect, which the hint named", view)
		}
	})
	t.Run("with no agent to connect", func(t *testing.T) {
		view := screenAfter(
			screenAfterParams{
				T:     t,
				Setup: setupFor(configWithAServerToFinish(t)),
				Load:  fromCatalog(c),
				Keys:  []string{"tab", "enter"},
			},
		)
		if !strings.HasPrefix(view, "Finish setting up these MCPs later") ||
			continueLine(view) != "> Continue   review and finish" {
			t.Errorf("view:\n%s\nwant Logins naming Review, which ends every run without agents", view)
		}
	})
}

type continueAfterParams struct {
	T       *testing.T
	Setup   initcmd.Setup
	Catalog catalog.Catalog
	Keys    []string
}

type screenAfterParams struct {
	T     *testing.T
	Setup initcmd.Setup
	Load  func() (catalog.Catalog, error)
	Keys  []string
}
