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

func continueAfter(t *testing.T, setup initcmd.Setup, c catalog.Catalog, keys ...string) string {
	t.Helper()
	return continueLine(screenAfter(t, setup, fromCatalog(c), keys...))
}

func screenAfter(t *testing.T, setup initcmd.Setup, load func() (catalog.Catalog, error), keys ...string) string {
	t.Helper()
	var view string
	_, err := Run(Params{Setup: setup, LoadCatalog: load, Program: func(m tea.Model) error {
		pressing(keys...)(m)
		view = shown(m.(*app))
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func connectingSetup(configDir string, list ...agents.Agent) initcmd.Setup {
	setup := setupFor(configDir, list...)
	setup.AgentsToConnect = list
	return setup
}

func entryWithAuth(name, auth string) catalog.Entry {
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
			if line := continueAfter(t, setup, catalog.Catalog{Entries: tc.entries}); line != tc.want {
				t.Errorf("Continue line on Import = %q, want %q", line, tc.want)
			}
		})
	}
}

func TestContinueHints_importNamesNoCatalogThatFailedToLoad(t *testing.T) {
	failing := func() (catalog.Catalog, error) { return catalog.Catalog{}, errors.New("offline") }
	view := screenAfter(t, setupFor(t.TempDir(), stdioAgent("Codex", "files")), failing)
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
			if line := continueAfter(t, setup, c, "space"); line != "  Continue" {
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
		if line := continueAfter(t, setup, c, "tab", "enter"); line != "> Continue   add mini to your agents" {
			t.Errorf("Continue line on Logins = %q, want add mini to your agents", line)
		}
		view := screenAfter(t, setup, fromCatalog(c), "tab", "enter", "tab", "enter")
		if !strings.Contains(view, "Connect mini to Codex") {
			t.Errorf("after Continue on Logins:\n%s\nwant Connect, which the hint named", view)
		}
	})
	t.Run("with no agent to connect", func(t *testing.T) {
		view := screenAfter(t, setupFor(configWithAServerToFinish(t)), fromCatalog(c), "tab", "enter")
		if !strings.HasPrefix(view, "Finish setting up these MCPs later") ||
			continueLine(view) != "> Continue   review and finish" {
			t.Errorf("view:\n%s\nwant Logins naming Review, which ends every run without agents", view)
		}
	})
}
