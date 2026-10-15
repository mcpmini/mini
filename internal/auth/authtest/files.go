package authtest

import (
	"cmp"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

const (
	DefaultServerURL = "https://mcp.example.com/mcp"
	DefaultTokenURL  = "https://as.example.com/token"
	// RegisteredClientID is the client a saved login names when its server config sets no client_id.
	RegisteredClientID = "registered-client"
)

// TokenFile saves Token as a login made for Server, so a provider or CLI built from Server uses it.
// Server's empty URL and token_url default to DefaultServerURL and DefaultTokenURL.
type TokenFile struct {
	ConfigDir  string
	ServerName string
	Server     config.ServerConfig
	Token      *oauth2.Token
}

type RegistrationFile struct {
	ConfigDir    string
	ServerName   string
	Registration *auth.Registration
}

func SaveToken(t testing.TB, p TokenFile) {
	t.Helper()
	if err := auth.SaveLogin(p.ConfigDir, p.ServerName, LoginFor(t, p.Server, p.Token)); err != nil {
		t.Fatalf("save login for %q: %v", p.ServerName, err)
	}
}

func LoginFor(t testing.TB, sc config.ServerConfig, token *oauth2.Token) *auth.Login {
	t.Helper()
	resource, err := auth.CanonicalResourceURI(cmp.Or(sc.URL, DefaultServerURL))
	if err != nil {
		t.Fatalf("canonical resource: %v", err)
	}
	login := &auth.Login{
		Resource: resource,
		TokenURL: DefaultTokenURL,
		Client:   auth.LoginClient{ID: RegisteredClientID},
		Token:    token,
	}
	if sc.Auth != nil {
		login.TokenURL = cmp.Or(sc.Auth.TokenURL, DefaultTokenURL)
		if sc.Auth.ClientID != "" {
			login.Client = auth.LoginClient{ID: sc.Auth.ClientID, Configured: true}
		}
	}
	return login
}

func SaveRegistration(t testing.TB, p RegistrationFile) {
	t.Helper()
	if err := auth.SaveRegistration(p.ConfigDir, p.ServerName, p.Registration); err != nil {
		t.Fatalf("save registration for %q: %v", p.ServerName, err)
	}
}
