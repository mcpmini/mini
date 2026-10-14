//go:build test

package provider_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/fileio"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func TestProvider_initialIneligibleLoginRequiresAuthorizationWithoutNetwork(t *testing.T) {
	cases := map[string]func(*auth.Login){
		"resource":       func(l *auth.Login) { l.Resource = "https://other.example.com/mcp" },
		"client_id":      func(l *auth.Login) { l.Client.ID = "another-client" },
		"client_source":  func(l *auth.Login) { l.Client = auth.Client{Source: auth.ClientRegistered, ID: "registered-client"} },
		"token_endpoint": func(l *auth.Login) { l.TokenURL = "https://other.example.com/token" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
			login, err := auth.LoadLogin(f.dir, "srv")
			if err != nil {
				t.Fatal(err)
			}
			mutate(login)
			lock := auth.TokenLock{ConfigDir: f.dir, ServerName: "srv", Clock: f.clock}
			if err = lock.Save(context.Background(), login); err != nil {
				t.Fatal(err)
			}
			original := testutil.ReadFile(t, filepath.Join(f.dir, "internal", "srv.token.json"))
			_, err = f.provider.Authorization(context.Background())
			if !errors.Is(err, transport.ErrReauthRequired) || !strings.Contains(err.Error(), "run mini auth srv") ||
				strings.Contains(err.Error(), "restart mini") {
				t.Fatalf("initial rejection=%v, want authorization remedy", err)
			}
			if f.endpoint.Hits.Load() != 0 {
				t.Fatal("ineligible login reached token endpoint")
			}
			if got := testutil.ReadFile(
				t,
				filepath.Join(f.dir, "internal", "srv.token.json"),
			); string(
				got,
			) != string(
				original,
			) {
				t.Fatal("rejection overwrote the saved login")
			}
		})
	}
}

func TestProvider_changedResourceIsRejectedDuring401AndExpiryAdoption(t *testing.T) {
	for _, expiry := range []bool{false, true} {
		t.Run(map[bool]string{false: "401", true: "expiry"}[expiry], func(t *testing.T) {
			f := newProviderFixture(t, providerSetup{Token: storedToken(clock.NewFake().Now().Add(10 * time.Minute))})
			if _, err := f.provider.Authorization(context.Background()); err != nil {
				t.Fatal(err)
			}
			login, err := auth.LoadLogin(f.dir, "srv")
			if err != nil {
				t.Fatal(err)
			}
			login.Resource = "https://other.example.com/mcp"
			login.Token.AccessToken = "external-access"
			lock := auth.TokenLock{ConfigDir: f.dir, ServerName: "srv", Clock: f.clock}
			if err = lock.Save(context.Background(), login); err != nil {
				t.Fatal(err)
			}
			if expiry {
				f.clock.Advance(11 * time.Minute)
				_, err = f.provider.Authorization(context.Background())
			} else {
				_, err = f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
			}
			if !errors.Is(err, transport.ErrReauthRequired) ||
				!strings.Contains(err.Error(), "configuration changed; restart mini") {
				t.Fatalf("adoption rejection=%v, want restart remedy", err)
			}
			if f.endpoint.Hits.Load() != 0 {
				t.Fatal("foreign login reached token endpoint")
			}
			saved, loadErr := auth.LoadLogin(f.dir, "srv")
			if loadErr != nil || saved.Token.AccessToken != "external-access" {
				t.Fatalf("foreign login was overwritten: %v,%v", saved, loadErr)
			}
		})
	}
}

func TestRefresh_protectedRereadRejectsMissingOrCorruptLoginWithoutExchangeOrSave(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "missing"}[missing], func(t *testing.T) {
			f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
			if _, err := f.provider.Authorization(context.Background()); err != nil {
				t.Fatal(err)
			}
			unlock, err := fileio.LockFile(
				context.Background(),
				f.clock,
				filepath.Join(f.dir, "internal", "srv.token.lock"),
			)
			if err != nil {
				t.Fatal(err)
			}
			release := sync.OnceFunc(unlock)
			defer release()
			refreshed := refreshInBackground(f.provider.RefreshAuthorization)
			testutil.WaitForTimers(t, f.clock, 1)
			path := filepath.Join(f.dir, "internal", "srv.token.json")
			if missing {
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				testutil.WriteFile(t, path, "{broken login")
			}
			release()
			f.clock.Advance(time.Second)
			result := <-refreshed
			if result.err == nil || result.bearer != "" {
				t.Fatalf("refresh=%q,%v, want rejection", result.bearer, result.err)
			}
			if f.endpoint.Hits.Load() != 0 {
				t.Fatal("protected reread failure reached token endpoint")
			}
			if missing {
				if _, err = os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("missing login recreated: %v", err)
				}
			} else if got := testutil.ReadFile(t, path); string(got) != "{broken login" {
				t.Fatalf("corrupt login overwritten: %q", got)
			}
		})
	}
}

func TestCommit_configuredClientUsesCurrentSecretWithoutPersistingIt(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	params.AuthConfig.ClientSecret = "first-configured-secret"
	params.AuthConfig.TokenEndpointAuthMethod = "client_secret_post"
	login := issuingLogin(params, auth.Client{Source: auth.ClientConfigured, ID: "cid"}, storedToken(time.Time{}))
	if err := f.registry.CommitAuthorizedToken(params, login); err != nil {
		t.Fatal(err)
	}
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatal(err)
	}
	f.endpoint.Mu.Lock()
	firstSecret := f.endpoint.LastSecret
	f.endpoint.Mu.Unlock()
	if firstSecret != "first-configured-secret" {
		t.Fatalf("first refresh secret=%q", firstSecret)
	}
	params.AuthConfig = params.AuthConfig.Clone()
	params.AuthConfig.ClientSecret = "current-configured-secret"
	login.Token.AccessToken = "reauthorized"
	if err = f.registry.CommitAuthorizedToken(params, login); err != nil {
		t.Fatal(err)
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer reauthorized"); err != nil {
		t.Fatal(err)
	}
	f.endpoint.Mu.Lock()
	currentSecret := f.endpoint.LastSecret
	f.endpoint.Mu.Unlock()
	if currentSecret != "current-configured-secret" {
		t.Fatalf("post-authorization refresh secret=%q", currentSecret)
	}
	saved := testutil.ReadFile(t, filepath.Join(f.dir, "internal", "srv.token.json"))
	if strings.Contains(string(saved), "configured-secret") {
		t.Fatal("configured secret persisted in login")
	}
}

func TestProvider_unsetEnvironmentRejectsSavedLoginBeforeNetwork(t *testing.T) {
	f := newCommitFixture(t)
	params := f.params()
	login := issuingLogin(params, auth.Client{Source: auth.ClientConfigured, ID: "cid"}, storedToken(time.Time{}))
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  f.dir,
			ServerName: "srv",
			Resource:   login.Resource,
			TokenURL:   login.TokenURL,
			Client:     login.Client,
			Token:      login.Token,
		},
	)
	params.UnsetEnv = errors.New("missing CLIENT_SECRET")
	p, err := f.registry.GetOrCreate(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err == nil {
		t.Fatal("unset environment authorized")
	}
	if f.endpoint.Hits.Load() != 0 {
		t.Fatal("unset environment reached token endpoint")
	}
}
