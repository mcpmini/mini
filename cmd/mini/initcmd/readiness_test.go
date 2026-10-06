//go:build test

package initcmd

import (
	"reflect"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestServerStatuses(t *testing.T) {
	dir := t.TempDir()
	oauth := func(name string) config.ServerConfig {
		return config.ServerConfig{
			Name:      name,
			Transport: "http",
			URL:       "https://" + name + ".example.com/mcp",
			Auth:      &config.AuthConfig{Type: config.AuthTypeOAuth2},
		}
	}
	for _, sc := range []config.ServerConfig{
		oauth("login"), oauth("loggedin"),
		{Name: "token", Transport: "http", URL: "https://token.example.com/mcp"},
		{Name: "keyed", Transport: "http", URL: "https://keyed.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer ${KEY}"}},
		{Name: "app", Transport: "http", URL: "https://app.example.com/mcp"},
		{Name: "local", Command: "run"},
		{Name: "off", Command: "run", Enabled: new(false)},
		{Name: "unset", Transport: "http", URL: "https://unset.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer ${MINI_TEST_UNSET}"}},
		{Name: "github", Command: "docker", Args: []string{"run", "github-mcp"}},
		{Name: "tok", Transport: "http", URL: "https://Token.example.com/mcp/"},
	} {
		configtest.WriteServer(t, dir, sc)
	}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "loggedin",
			Token:      &oauth2.Token{AccessToken: "t", Expiry: time.Now().Add(time.Hour)},
		},
	)
	t.Setenv("KEY", "synthetic")
	entries := []catalog.Entry{
		{
			Name:     "token",
			URL:      "https://token.example.com/mcp",
			Auth:     catalog.AuthToken,
			SetupURL: "https://token.example.com/new",
		},
		{
			Name:     "keyed",
			URL:      "https://keyed.example.com/mcp",
			Auth:     catalog.AuthToken,
			SetupURL: "https://keyed.example.com/new",
		},
		{
			Name:     "app",
			URL:      "https://app.example.com/mcp",
			Auth:     catalog.AuthOAuth2App,
			SetupURL: "https://app.example.com/apps",
		},
		{
			Name:     "github",
			URL:      "https://api.githubcopilot.com/mcp/",
			Auth:     catalog.AuthToken,
			SetupURL: "https://github.com/settings/tokens",
		},
	}

	statuses, err := ServerStatuses(dir, entries)

	want := []ServerStatus{
		{Name: "app", Readiness: NeedsOwnApp, SetupURL: "https://app.example.com/apps"},
		{Name: "github", Readiness: Ready},
		{Name: "keyed", Readiness: Ready, SetupURL: "https://keyed.example.com/new"},
		{Name: "local", Readiness: Ready},
		{Name: "loggedin", Readiness: Ready},
		{Name: "login", Readiness: NeedsLogin},
		{Name: "tok", Readiness: NeedsToken, SetupURL: "https://token.example.com/new"},
		{Name: "token", Readiness: NeedsToken, SetupURL: "https://token.example.com/new"},
		{
			Name:      "unset",
			Readiness: NeedsEnv,
			UnsetEnv:  &config.UnsetEnvError{Field: "headers.Authorization", Names: []string{"MINI_TEST_UNSET"}},
		},
	}
	if err != nil || !reflect.DeepEqual(statuses, want) {
		t.Errorf("statuses = %+v, %v\nwant %+v", statuses, err, want)
	}
}
