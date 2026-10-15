//go:build test

package provider_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestProviderRegistry_sameServer_returnsSameProvider(t *testing.T) {
	registry := provider.NewRegistry()
	params := provider.Params{
		AuthConfig: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "cid",
			TokenURL: "http://localhost:1/token",
		},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		Clock:      clock.NewFake(),
	}
	first, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("registry returned different providers for the same server")
	}
}

func TestProviderRegistry_changedServerURL_rejectedAndOriginalKept(t *testing.T) {
	params := provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  t.TempDir(),
		ServerName: "srv",
		ServerURL:  "https://mcp.example.com/mcp",
		Clock:      clock.NewFake(),
	}
	registry := provider.NewRegistry()
	original, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	changed := params
	changed.ServerURL = "https://other.example.com/mcp"
	if _, registryErr := registry.GetOrCreate(changed); registryErr == nil {
		t.Fatal("incompatible provider identity was accepted")
	}
	again, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if original != again {
		t.Fatal("rejected configuration mutated the registered provider")
	}
}

func TestProviderRegistry_redialAfterBrowserLogin_reusesProvider(t *testing.T) {
	params := provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
		ConfigDir:  t.TempDir(), ServerName: "srv", ServerURL: testServerURL, Clock: clock.NewFake(),
	}
	reg := provider.NewRegistry()
	first, err := reg.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	tok := &oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh"}
	if commitErr := reg.CommitAuthorizedToken(params, registeredLogin(t, params, tok, "discovered")); commitErr != nil {
		t.Fatalf("CommitAuthorizedToken: %v", commitErr)
	}
	second, err := reg.GetOrCreate(params)
	if err != nil {
		t.Fatalf("re-dial rejected: %v", err)
	}
	if first != second {
		t.Fatal("re-dial returned a different provider")
	}
}

func TestProviderRegistry_close_abortsInFlightRefresh(t *testing.T) {
	dir := t.TempDir()
	epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: epoch.Add(-time.Second)}

	endpoint := authtest.NewTokenServer(t)
	received, release := gateNextTokenRequest(endpoint)
	t.Cleanup(func() { release() })

	params := provider.Params{
		AuthConfig: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "c",
			TokenURL: endpoint.Srv.URL + "/token",
		},
		ConfigDir:  dir,
		ServerName: "srv",
		ServerURL:  testServerURL,
		Clock:      clock.NewFakeAt(epoch),
	}
	saveLoginFor(t, params, expired)
	registry := provider.NewRegistry()
	prov, err := registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		prov.Authorization(context.Background()) //nolint:errcheck
	}()
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("token endpoint not reached within 5s")
	}

	registry.Close()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("registry.Close() did not abort in-flight refresh within 5s")
	}
}

func TestProviderRegistry_forget(t *testing.T) {
	oauthParams := func(dir, serverURL string) provider.Params {
		return provider.Params{
			AuthConfig: &config.AuthConfig{
				Type:     config.AuthTypeOAuth2,
				ClientID: "cid",
				TokenURL: "http://localhost:1/token",
			},
			ConfigDir:  dir,
			ServerName: "srv",
			ServerURL:  serverURL,
			Clock:      clock.NewFake(),
		}
	}

	t.Run("the next provider for the name loads its token from disk, at any URL", func(t *testing.T) {
		for _, nextURL := range []string{testServerURL, "https://other.example.com/mcp"} {
			dir := t.TempDir()
			saveLoginFor(t, oauthParams(dir, testServerURL), &oauth2.Token{AccessToken: "old"})
			registry := provider.NewRegistry()
			old, err := registry.GetOrCreate(oauthParams(dir, testServerURL))
			if err != nil {
				t.Fatal(err)
			}
			if _, authorizationErr := old.Authorization(context.Background()); authorizationErr != nil {
				t.Fatal(authorizationErr)
			}
			if deleteErr := auth.DeleteCredentials(dir, "srv"); deleteErr != nil {
				t.Fatal(deleteErr)
			}

			registry.Forget("srv")
			next, err := registry.GetOrCreate(oauthParams(dir, nextURL))
			if err != nil {
				t.Fatalf("GetOrCreate at %s after Forget: %v", nextURL, err)
			}

			if got, err := next.Authorization(context.Background()); err == nil {
				t.Errorf("provider at %s authorized with %q, want the deleted token gone", nextURL, got)
			}
		}
	})

	t.Run("the forgotten provider no longer authorizes", func(t *testing.T) {
		dir := t.TempDir()
		saveLoginFor(t, oauthParams(dir, testServerURL), &oauth2.Token{AccessToken: "old"})
		registry := provider.NewRegistry()
		old, err := registry.GetOrCreate(oauthParams(dir, testServerURL))
		if err != nil {
			t.Fatal(err)
		}

		registry.Forget("srv")

		if got, err := old.Authorization(context.Background()); err == nil {
			t.Errorf("forgotten provider authorized with %q", got)
		}
	})

	t.Run("an in-flight refresh is aborted and does not save its token", func(t *testing.T) {
		dir := t.TempDir()
		epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		endpoint := authtest.NewTokenServer(t)
		received, release := gateNextTokenRequest(endpoint)
		t.Cleanup(func() { release() })
		params := oauthParams(dir, testServerURL)
		params.AuthConfig.TokenURL = endpoint.Srv.URL + "/token"
		params.Clock = clock.NewFakeAt(epoch)
		saveLoginFor(t, params, &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: epoch.Add(-time.Second)})
		registry := provider.NewRegistry()
		old, err := registry.GetOrCreate(params)
		if err != nil {
			t.Fatal(err)
		}
		go old.Authorization(context.Background()) //nolint:errcheck // the refresh's outcome is read from disk below
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			t.Fatal("token endpoint not reached within 5s")
		}

		forgotten := make(chan struct{})
		go func() {
			registry.Forget("srv")
			close(forgotten)
		}()
		select {
		case <-forgotten:
		case <-time.After(5 * time.Second):
			t.Fatal("Forget did not abort the in-flight refresh within 5s")
		}

		saved, err := loadSavedToken(dir)
		if err != nil {
			t.Fatal(err)
		}
		if saved.AccessToken != "old" {
			t.Errorf("saved token = %q after Forget, want the refresh's token never saved", saved.AccessToken)
		}
	})
}
