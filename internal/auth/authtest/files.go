package authtest

import (
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
)

type TokenFile struct {
	ConfigDir  string
	ServerName string
	Token      *oauth2.Token
}

type RegistrationFile struct {
	ConfigDir    string
	ServerName   string
	Registration *auth.Registration
}

func SaveToken(t testing.TB, p TokenFile) {
	t.Helper()
	if err := auth.Save(p.ConfigDir, p.ServerName, p.Token); err != nil {
		t.Fatalf("save token for %q: %v", p.ServerName, err)
	}
}

func SaveRegistration(t testing.TB, p RegistrationFile) {
	t.Helper()
	if err := auth.SaveRegistration(p.ConfigDir, p.ServerName, p.Registration); err != nil {
		t.Fatalf("save registration for %q: %v", p.ServerName, err)
	}
}
