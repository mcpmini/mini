//go:build test

package provider_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

// Two providers on one config dir stand in for two mini processes: the token lock is per open
// file, so they exclude each other as separate processes would.
func TestRefreshAuthorization_secondProcessAdoptsTheFirstsRotation(t *testing.T) {
	first := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	second, secondClock := secondProcess(t, first)
	received, release := gateNextTokenRequest(t, first.endpoint)
	firstDone := refreshInBackground(first.provider.RefreshAuthorization)
	<-received
	secondDone := refreshInBackground(second.RefreshAuthorization)
	testutil.WaitForTimers(t, secondClock, 1)

	release()
	assertBearer(t, <-firstDone, "Bearer new-access")
	secondClock.Advance(time.Second)
	assertBearer(t, <-secondDone, "Bearer new-access")
	assertOneTokenRequest(t, first.endpoint)
}

func TestAuthorization_expiringToken_secondProcessAdoptsTheFirstsProactiveRotation(t *testing.T) {
	expiring := storedToken(clock.NewFake().Now().Add(time.Minute))
	first := newProviderFixture(t, providerSetup{Token: expiring})
	second, secondClock := secondProcess(t, first)
	received, release := gateNextTokenRequest(t, first.endpoint)
	firstDone := authorizeInBackground(first.provider)
	<-received
	secondDone := authorizeInBackground(second)
	testutil.WaitForTimers(t, secondClock, 1)

	release()
	assertBearer(t, <-firstDone, "Bearer new-access")
	secondClock.Advance(time.Second)
	assertBearer(t, <-secondDone, "Bearer new-access")
	assertOneTokenRequest(t, first.endpoint)
}

func secondProcess(t *testing.T, first *providerFixture) (transport.AuthorizationProvider, *clock.Fake) {
	t.Helper()
	secondClock := clock.NewFake()
	second, err := provider.New(provider.Params{
		ServerURL: "https://resource.example.com/mcp",
		AuthConfig: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: first.endpoint.Srv.URL + "/token",
		},
		ConfigDir: first.dir, ServerName: "srv", Clock: secondClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return second, secondClock
}

func assertOneTokenRequest(t *testing.T, endpoint *authtest.TokenServer) {
	t.Helper()
	if hits := endpoint.Hits.Load(); hits != 1 {
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
	go func() {
		saved <- login.Save(context.Background(), &auth.Login{Resource: "https://resource.example.com/mcp", TokenURL: f.endpoint.Srv.URL + "/token", Client: auth.Client{Source: auth.ClientConfigured, ID: "cid"}, Token: &oauth2.Token{AccessToken: "fresh-login"}})
	}()
	testutil.WaitForTimers(t, loginClock, 1)

	release()
	<-refreshed
	loginClock.Advance(time.Second)
	if err := <-saved; err != nil {
		t.Fatalf("login save: %v", err)
	}
	if got, err := auth.LoadLogin(f.dir, "srv"); err != nil || got.Token.AccessToken != "fresh-login" {
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

func authorizeInBackground(p transport.AuthorizationProvider) <-chan refreshResult {
	return refreshInBackground(func(ctx context.Context, _ string) (string, error) {
		return p.Authorization(ctx)
	})
}

func assertBearer(t *testing.T, got refreshResult, want string) {
	t.Helper()
	if got.err != nil || got.bearer != want {
		t.Errorf("RefreshAuthorization = %q, %v; want %q", got.bearer, got.err, want)
	}
}
