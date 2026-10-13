//go:build test

package provider_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

// Two providers on one config dir stand in for two mini processes: the token lock is per open
// file, so they exclude each other as separate processes would.
func TestRefreshAuthorization_secondProcessAdoptsTheFirstsRotation(t *testing.T) {
	first := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	secondClock := clock.NewFake()
	second, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: first.endpoint.Srv.URL + "/token",
		},
		ConfigDir: first.dir, ServerName: "srv", Clock: secondClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	received, release := gateNextTokenRequest(t, first.endpoint)
	firstDone := refreshInBackground(first.provider.RefreshAuthorization)
	<-received
	secondDone := refreshInBackground(second.RefreshAuthorization)
	testutil.WaitForTimers(t, secondClock, 1)

	release()
	assertBearer(t, <-firstDone, "Bearer new-access")
	secondClock.Advance(time.Second)
	assertBearer(t, <-secondDone, "Bearer new-access")
	if hits := first.endpoint.Hits.Load(); hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1: the rotated refresh token was presented twice", hits)
	}
}

func TestRefreshAuthorization_loginSavedDuringARefreshIsKept(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	received, release := gateNextTokenRequest(t, f.endpoint)
	refreshed := refreshInBackground(f.provider.RefreshAuthorization)
	<-received
	loginClock := clock.NewFake()
	login := auth.TokenLock{ConfigDir: f.dir, ServerName: "srv", Clock: loginClock}
	saved := make(chan error, 1)
	go func() { saved <- login.Save(context.Background(), &oauth2.Token{AccessToken: "fresh-login"}) }()
	testutil.WaitForTimers(t, loginClock, 1)

	release()
	<-refreshed
	loginClock.Advance(time.Second)
	if err := <-saved; err != nil {
		t.Fatalf("login save: %v", err)
	}
	if got, err := auth.Load(f.dir, "srv"); err != nil || got.AccessToken != "fresh-login" {
		t.Errorf("token on disk = %v, %v; want the login saved last", got, err)
	}
}

type refreshResult struct {
	bearer string
	err    error
}

func refreshInBackground(refresh func(context.Context, string) (string, error)) <-chan refreshResult {
	done := make(chan refreshResult, 1)
	go func() {
		bearer, err := refresh(context.Background(), "Bearer stored-access")
		done <- refreshResult{bearer: bearer, err: err}
	}()
	return done
}

func assertBearer(t *testing.T, got refreshResult, want string) {
	t.Helper()
	if got.err != nil || got.bearer != want {
		t.Errorf("RefreshAuthorization = %q, %v; want %q", got.bearer, got.err, want)
	}
}
