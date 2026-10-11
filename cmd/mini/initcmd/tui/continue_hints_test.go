//go:build test

package tui

import (
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
	var line string
	_, err := Run(Params{Setup: setup, LoadCatalog: fromCatalog(c), Program: func(m tea.Model) error {
		pressing(keys...)(m)
		line = continueLine(shown(m.(*app)))
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func connectingSetup(configDir string, list ...agents.Agent) initcmd.Setup {
	setup := setupFor(configDir, list...)
	setup.AgentsToConnect = list
	return setup
}

func httpAgent(name, url string) agents.Agent {
	entries := map[string]agents.Server{"remote": {Config: config.ServerConfig{Transport: "http", URL: url}}}
	return agents.Agent{Name: name, ConfigPath: name, Read: func(string) (map[string]agents.Server, error) {
		return entries, nil
	}}
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

func TestContinueHints_importWithACatalogSaysBrowsePopularMCPs(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	line := continueAfter(t, setupFor(t.TempDir(), stdioAgent("Codex", "files")), c)
	if want := "  Continue   browse popular MCPs"; line != want {
		t.Errorf("Continue line = %q, want %q", line, want)
	}
}

func TestContinueHints_catalogWithAnOAuthPickSaysLogInToYourMCPs(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{oauthEntry("sentry")}}
	line := continueAfter(t, setupFor(t.TempDir()), c, "space")
	if want := "  Continue   log in to your MCPs"; line != want {
		t.Errorf("Continue line = %q, want %q", line, want)
	}
}

func TestContinueHints_catalogWithOnlyNoAuthPicksAndAnAgentToConnectSaysAddMini(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	line := continueAfter(t, connectingSetup(t.TempDir(), stdioAgent("Codex")), c, "space")
	if want := "  Continue   add mini to your agents"; line != want {
		t.Errorf("Continue line = %q, want %q", line, want)
	}
}

func TestContinueHints_catalogWithNothingTickedSaysNothingWithNoAgentsAndNoLogins(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	if line := continueAfter(t, setupFor(t.TempDir()), c); line != "  Continue" {
		t.Errorf("Continue line = %q, want plain Continue", line)
	}
}

func TestContinueHints_catalogSaysLogInAfterGoingBackFromLoginsWhichNeedsALogin(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	line := continueAfter(t, setupFor(configWithAServerToFinish(t)), c, "tab", "enter", "esc")
	if want := "  Continue   log in to your MCPs"; line != want {
		t.Errorf("Continue line on Catalog after Logins = %q, want %q", line, want)
	}
}

func TestContinueHints_catalogSaysLogInForAnImportThatMayNeedOAuth(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	setup := setupFor(t.TempDir(), httpAgent("Codex", "https://remote.example.com/mcp"))
	if line := continueAfter(t, setup, c, "tab", "enter"); line != "  Continue   log in to your MCPs" {
		t.Errorf("Continue line on Catalog = %q, want log in to your MCPs", line)
	}
}

func TestContinueHints_loginsSaysAddMiniOnlyWhenAnAgentWillBeListed(t *testing.T) {
	c := catalog.Catalog{Entries: []catalog.Entry{entryWithAuth("sentry", catalog.AuthNone)}}
	t.Run("with an agent without mini", func(t *testing.T) {
		line := continueAfter(t, connectingSetup(configWithAServerToFinish(t), stdioAgent("Codex")), c, "tab", "enter")
		if !strings.HasSuffix(line, "Continue   add mini to your agents") {
			t.Errorf("Continue line on Logins = %q, want add mini to your agents", line)
		}
	})
	t.Run("with no agent to connect", func(t *testing.T) {
		line := continueAfter(t, setupFor(configWithAServerToFinish(t)), c, "tab", "enter")
		if strings.Contains(line, "add mini") {
			t.Errorf("Continue line on Logins = %q, want no hint", line)
		}
	})
}
