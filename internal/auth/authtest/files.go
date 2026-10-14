package authtest

import (
	"context"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
)

type TokenFile struct {
	ConfigDir  string
	ServerName string
	Resource   string
	TokenURL   string
	Client     auth.Client
	Token      *oauth2.Token
}

type RegistrationFile struct {
	ConfigDir    string
	ServerName   string
	Registration *auth.Registration
}

func SaveToken(t testing.TB, p TokenFile) {
	t.Helper()
	lock := auth.TokenLock{ConfigDir: p.ConfigDir, ServerName: p.ServerName, Clock: clock.System()}
	login := &auth.Login{Resource: p.Resource, TokenURL: p.TokenURL, Client: p.Client, Token: p.Token}
	if err := lock.Save(context.Background(), login); err != nil {
		t.Fatalf("save token for %q: %v", p.ServerName, err)
	}
}

func SaveRegistration(t testing.TB, p RegistrationFile) {
	t.Helper()
	if err := auth.SaveRegistration(p.ConfigDir, p.ServerName, p.Registration); err != nil {
		t.Fatalf("save registration for %q: %v", p.ServerName, err)
	}
}
