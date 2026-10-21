package authtest

import (
	"cmp"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

// A config that leaves token_url or client_id to discovery matches a login with any value there.
const (
	discoveredTokenURL = "https://as.example.com/token"
	registeredClientID = "registered-client"
)

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

// SaveToken saves Token as a login made for Server, so a provider or CLI built from Server uses it.
func SaveToken(t testing.TB, p TokenFile) {
	t.Helper()
	if err := auth.SaveLogin(p.ConfigDir, p.ServerName, LoginFor(t, p.Server, p.Token)); err != nil {
		t.Fatalf("save login for %q: %v", p.ServerName, err)
	}
}

func LoginFor(t testing.TB, sc config.ServerConfig, token *oauth2.Token) *auth.Login {
	t.Helper()
	if sc.URL == "" {
		t.Fatal("authtest: a login needs the server config it was made for; set its URL")
	}
	resource, err := auth.CanonicalResourceURI(sc.URL)
	if err != nil {
		t.Fatalf("canonical resource: %v", err)
	}
	login := &auth.Login{
		Resource: resource,
		Issuer:   "https://as.example.com",
		TokenURL: discoveredTokenURL,
		Client:   auth.LoginClient{ID: registeredClientID},
		Token:    token,
	}
	if sc.Auth != nil {
		login.Issuer = cmp.Or(sc.Auth.Issuer, login.Issuer)
		login.TokenURL = cmp.Or(sc.Auth.TokenURL, discoveredTokenURL)
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
