//go:build test

package initcmd

import (
	"reflect"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestServerStatuses(t *testing.T) {
	dir := t.TempDir()
	oauth := func(name, clientID string) config.ServerConfig {
		return config.ServerConfig{
			Name:      name,
			Transport: "http",
			URL:       "https://" + name + ".example.com/mcp",
			Auth:      &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: clientID},
		}
	}
	for _, sc := range []config.ServerConfig{
		oauth("login", ""), oauth("loggedin", ""), oauth("noregistration", ""), oauth("ownapp", "my-app"),
		oauth("appplaceholder", clientIDPlaceholder),
		{Name: "plain", Transport: "http", URL: "https://plain.example.com/mcp"},
		{Name: "refused", Transport: "http", URL: "https://refused.example.com/mcp"},
		{Name: "keyed", Transport: "http", URL: "https://keyed.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer ${KEY}"}},
		{
			Name: "tokenplaceholder", Transport: "http", URL: "https://tokenplaceholder.example.com/mcp",
			Headers: map[string]string{"Authorization": "Bearer " + tokenPlaceholder},
		},
		{Name: "local", Command: "run"},
		{Name: "off", Command: "run", Enabled: new(false)},
		{Name: "unset", Transport: "http", URL: "https://unset.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer ${MINI_TEST_UNSET}"}},
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

	statuses, err := ServerStatuses(dir, CheckFindings{
		NoSelfRegistration: map[string]bool{"noregistration": true, "ownapp": true, "loggedin": true},
		NeedsCredential:    map[string]bool{"refused": true},
	})

	want := []ServerStatus{
		{Name: "appplaceholder", Readiness: NeedsOwnAppState{}},
		{Name: "keyed", Readiness: ReadyState{}},
		{Name: "local", Readiness: ReadyState{}},
		{Name: "loggedin", Readiness: ReadyState{}},
		{Name: "login", Readiness: NeedsLoginState{}},
		{Name: "noregistration", Readiness: NeedsOwnAppState{}},
		{Name: "ownapp", Readiness: NeedsLoginState{}},
		{Name: "plain", Readiness: ReadyState{}},
		{Name: "refused", Readiness: NeedsTokenState{}},
		{Name: "tokenplaceholder", Readiness: NeedsTokenState{}},
		{Name: "unset", Readiness: NeedsEnvState{Names: []string{"MINI_TEST_UNSET"}, Field: "headers.Authorization"}},
	}
	if err != nil || !reflect.DeepEqual(statuses, want) {
		t.Errorf("statuses = %+v, %v\nwant %+v", statuses, err, want)
	}
}

func TestServerStatusStepZeroReadinessNeedsNoStep(t *testing.T) {
	if got := (ServerStatus{}).Step("server.yaml", "mini auth server"); got != "" {
		t.Errorf("zero status step = %q, want none", got)
	}
}
