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
	oauth := func(name, clientID string) config.ServerConfig {
		return config.ServerConfig{
			Name:      name,
			Transport: "http",
			URL:       "https://" + name + ".example.com/mcp",
			Auth:      &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: clientID},
		}
	}
	for _, sc := range []config.ServerConfig{
		oauth("login", ""), oauth("loggedin", ""), oauth("moved", ""), oauth("noregistration", ""), oauth("ownapp", "my-app"),
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
	valid := &oauth2.Token{AccessToken: "t", Expiry: time.Now().Add(time.Hour)}
	authtest.SaveToken(
		t,
		authtest.TokenFile{ConfigDir: dir, ServerName: "loggedin", Server: oauth("loggedin", ""), Token: valid},
	)
	madeForOldURL := oauth("moved", "")
	madeForOldURL.URL = "https://old.example.com/mcp"
	authtest.SaveToken(t, authtest.TokenFile{ConfigDir: dir, ServerName: "moved", Server: madeForOldURL, Token: valid})
	t.Setenv("KEY", "synthetic")

	statuses, err := ServerStatuses(
		ServerStatusParams{
			ConfigDir:   dir,
			CatalogAuth: map[string]catalog.AuthKind{"loggedin": catalog.AuthToken, "ownapp": catalog.AuthToken},
			Findings: CheckFindings{
				NoSelfRegistration: map[string]bool{"noregistration": true, "ownapp": true, "loggedin": true},
				NeedsCredential:    map[string]bool{"refused": true},
			},
		},
	)

	want := []ServerStatus{
		{Name: "appplaceholder", URL: "https://appplaceholder.example.com/mcp", Readiness: NeedsOwnAppState{}},
		{Name: "keyed", URL: "https://keyed.example.com/mcp", Readiness: ReadyState{}},
		{Name: "local", Readiness: ReadyState{}},
		{Name: "loggedin", URL: "https://loggedin.example.com/mcp", Readiness: ReadyState{}},
		{Name: "login", URL: "https://login.example.com/mcp", Readiness: NeedsLoginState{}},
		{Name: "moved", URL: "https://moved.example.com/mcp", Readiness: NeedsLoginState{}},
		{Name: "noregistration", URL: "https://noregistration.example.com/mcp", Readiness: NeedsOwnAppState{}},
		{Name: "ownapp", URL: "https://ownapp.example.com/mcp", Readiness: NeedsLoginState{}},
		{Name: "plain", URL: "https://plain.example.com/mcp", Readiness: ReadyState{}},
		{Name: "refused", URL: "https://refused.example.com/mcp", Readiness: NeedsTokenState{}},
		{Name: "tokenplaceholder", URL: "https://tokenplaceholder.example.com/mcp", Readiness: NeedsTokenState{}},
		{
			Name:      "unset",
			URL:       "https://unset.example.com/mcp",
			Readiness: NeedsEnvState{Names: []string{"MINI_TEST_UNSET"}, Field: "headers.Authorization"},
		},
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

func TestServerStatuses_catalogAuthOnlyChoosesTheFallback(t *testing.T) {
	for _, tt := range []struct {
		name           string
		kind           catalog.AuthKind
		noRegistration bool
		want           Readiness
	}{
		{name: "token without registration", kind: catalog.AuthToken, noRegistration: true, want: NeedsTokenState{}},
		{name: "app without registration", kind: catalog.AuthOAuth2App, noRegistration: true, want: NeedsOwnAppState{}},
		{name: "unknown without registration", noRegistration: true, want: NeedsOwnAppState{}},
		{name: "token with registration", kind: catalog.AuthToken, want: NeedsLoginState{}},
		{name: "app with registration", kind: catalog.AuthOAuth2App, want: NeedsLoginState{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configtest.WriteServer(t, dir, oauthServer("service", ""))
			statuses, err := ServerStatuses(ServerStatusParams{
				ConfigDir:   dir,
				Findings:    CheckFindings{NoSelfRegistration: map[string]bool{"service": tt.noRegistration}},
				CatalogAuth: map[string]catalog.AuthKind{"service": tt.kind},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := statusNamed(t, statuses, "service").Readiness; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("readiness = %T, want %T", got, tt.want)
			}
		})
	}
}
