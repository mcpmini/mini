//go:build test

package provider_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestRefreshAuthorization_rotatedRefreshToken_isPersisted(t *testing.T) {
	f := newProviderFixture(t, providerSetup{
		Token: storedToken(time.Time{}),
		Auth:  &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", ResourceURL: "https://resource.example/mcp"},
	})
	got, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("header = %q, want refreshed token", got)
	}
	f.endpoint.Mu.Lock()
	grant, refresh, resource := f.endpoint.LastGrant, f.endpoint.LastRefresh, f.endpoint.LastResource
	f.endpoint.Mu.Unlock()
	if grant != "refresh_token" || refresh != "stored-refresh" {
		t.Errorf("refresh used grant=%q token=%q", grant, refresh)
	}
	if resource != "https://resource.example/mcp" {
		t.Errorf("refresh resource = %q", resource)
	}
	saved, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatalf("Load persisted token: %v", err)
	}
	if saved.AccessToken != "new-access" || saved.RefreshToken != "rotated-refresh" {
		t.Errorf("persisted access=%q refresh=%q, want rotated pair", saved.AccessToken, saved.RefreshToken)
	}
}

func TestRefreshAuthorization_tokenEndpoint503_returnsTransientError(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.Status.Store(http.StatusServiceUnavailable)
	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected refresh failure")
	}
	if strings.Contains(err.Error(), "mini auth") {
		t.Errorf("transient error should not name mini auth remedy, got: %v", err)
	}
	if !strings.Contains(err.Error(), "(transient)") {
		t.Errorf("transient error should say (transient), got: %v", err)
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("503 must not be classified as needing re-auth: %v", err)
	}
}

func TestRefreshAuthorization_saveFails_keepsRotatedTokenInMemory(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	internal := f.dir + "/internal"
	if err := os.Chmod(internal, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(internal, 0700) }) //nolint:errcheck

	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("refresh must succeed despite persist failure: %v", err)
	}
	got, err := f.provider.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization after persist failure: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("next call must use rotated in-memory token, got %q", got)
	}
	if hits := f.endpoint.Hits.Load(); hits != 1 {
		t.Errorf("rotated token must be reused without another refresh, got %d hits", hits)
	}

	f.endpoint.AccessToken, f.endpoint.RefreshToken = "second-access", "second-refresh"
	os.Chmod(internal, 0700) //nolint:errcheck
	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer new-access"); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	f.endpoint.Mu.Lock()
	lastRefresh := f.endpoint.LastRefresh
	f.endpoint.Mu.Unlock()
	if lastRefresh != "rotated-refresh" {
		t.Errorf("second refresh must use the rotated refresh token, sent %q", lastRefresh)
	}
	saved, err := auth.Load(f.dir, "srv")
	if err != nil {
		t.Fatalf("Load after persist retry: %v", err)
	}
	if saved.AccessToken != "second-access" || saved.RefreshToken != "second-refresh" {
		t.Errorf("persist must be retried on next refresh, got access=%q refresh=%q", saved.AccessToken, saved.RefreshToken)
	}
}

func TestRefreshAuthorization_newerStoredToken_usedWithoutRefreshing(t *testing.T) {
	dir := t.TempDir()
	oldToken := storedToken(time.Time{})
	if err := auth.Save(dir, "srv", oldToken); err != nil {
		t.Fatal(err)
	}
	endpoint := authtest.NewTokenServer(t)
	prov, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.Srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", ServerURL: "https://mcp.example.com/mcp", Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prov.Authorization(context.Background()); err != nil {
		t.Fatal(err)
	}
	external := &oauth2.Token{AccessToken: "external-access", RefreshToken: "external-refresh"}
	if err := auth.Save(dir, "srv", external); err != nil {
		t.Fatal(err)
	}
	got, err := prov.RefreshAuthorization(context.Background(), "Bearer "+oldToken.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer external-access" {
		t.Fatalf("Authorization = %q, want external token", got)
	}
	if endpoint.Hits.Load() != 0 {
		t.Fatalf("token endpoint hits = %d, want disk handoff without refresh", endpoint.Hits.Load())
	}
}

func TestRefreshAuthorization_concurrent401s_refreshOnce(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
			if err != nil {
				t.Error(err)
				return
			}
			results <- v
		}()
	}
	wg.Wait()
	close(results)
	if hits := f.endpoint.Hits.Load(); hits != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1", hits)
	}
	for v := range results {
		if v != "Bearer new-access" {
			t.Errorf("got %q, want Bearer new-access", v)
		}
	}
}

func TestRefreshAuthorization_callerCancelled_stillPersistsRotatedToken(t *testing.T) {
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: "old-access", RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatal(err)
	}
	endpoint := authtest.NewTokenServer(t)
	endpoint.AccessToken = "rotated-access"
	endpoint.RefreshToken = "rotated-refresh"
	received, release := gateNextTokenRequest(endpoint)

	p, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.Srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	refreshDone := make(chan error, 1)
	go func() {
		_, err := p.RefreshAuthorization(ctx, "Bearer old-access")
		refreshDone <- err
	}()
	<-received
	cancel()
	release()

	if err := <-refreshDone; err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	got, err := p.Authorization(context.Background())
	if err != nil {
		t.Fatalf("Authorization: %v", err)
	}
	if got != "Bearer rotated-access" {
		t.Errorf("Authorization = %q, want rotated token", got)
	}
	saved, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if saved.RefreshToken != "rotated-refresh" {
		t.Errorf("persisted refresh = %q, want rotated-refresh", saved.RefreshToken)
	}
	if endpoint.Hits.Load() != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (rotated token reused)", endpoint.Hits.Load())
	}
}

func TestRefreshAuthorization_noTokenURL_returnsReauthRemedy(t *testing.T) {
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{AccessToken: "tok", RefreshToken: "ref"}); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid"},
		ConfigDir:  dir, ServerName: "srv", Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.RefreshAuthorization(context.Background(), "Bearer tok")
	if err == nil {
		t.Fatal("expected error for missing token URL")
	}
	if !strings.Contains(err.Error(), "mini auth srv") {
		t.Errorf("error should name remedy, got: %v", err)
	}
}

func TestRefreshAuthorization_resourceFallback_canonicalizesServerURL(t *testing.T) {
	cases := []struct {
		name         string
		serverURL    string
		resourceURL  string
		wantResource string
	}{
		{"ServerURL canonicalized when ResourceURL empty", "HTTPS://Example.COM:443/mcp", "", "https://example.com/mcp"},
		{"ResourceURL wins when both set", "HTTPS://Example.COM:443/mcp", "https://other.example/api", "https://other.example/api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
				t.Fatal(err)
			}
			endpoint := authtest.NewTokenServer(t)
			p, err := provider.New(provider.Params{
				AuthConfig: &config.AuthConfig{
					Type: config.AuthTypeOAuth2, ClientID: "cid",
					TokenURL: endpoint.Srv.URL + "/token", ResourceURL: tc.resourceURL,
				},
				ConfigDir: dir, ServerName: "srv", ServerURL: tc.serverURL, Clock: clock.NewFake(),
			})
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
				t.Fatalf("RefreshAuthorization: %v", err)
			}
			endpoint.Mu.Lock()
			got := endpoint.LastResource
			endpoint.Mu.Unlock()
			if got != tc.wantResource {
				t.Errorf("resource = %q, want %q", got, tc.wantResource)
			}
		})
	}
}

func TestRefreshAuthorization_saveFailsAfterEarlierSave_keepsNewestTokenOnReload(t *testing.T) {
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", storedToken(time.Time{})); err != nil {
		t.Fatal(err)
	}
	endpoint := authtest.NewTokenServer(t)
	endpoint.AccessToken, endpoint.RefreshToken = "t2-access", "t2-refresh"
	p, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "cid", TokenURL: endpoint.Srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clock.NewFake(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("refresh 1: %v", err)
	}
	endpoint.AccessToken, endpoint.RefreshToken = "t3-access", "t3-refresh"
	internal := dir + "/internal"
	if err := os.Chmod(internal, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(internal, 0700) }) //nolint:errcheck
	if _, err := p.RefreshAuthorization(context.Background(), "Bearer t2-access"); err != nil {
		t.Fatalf("refresh 2: %v", err)
	}
	os.Chmod(internal, 0700) //nolint:errcheck
	endpoint.AccessToken, endpoint.RefreshToken = "t4-access", "t4-refresh"
	got, err := p.RefreshAuthorization(context.Background(), "Bearer t3-access")
	if err != nil {
		t.Fatalf("refresh 3: %v", err)
	}
	if got != "Bearer t4-access" {
		t.Errorf("refresh 3 = %q, want Bearer t4-access (must not regress to t2 on disk)", got)
	}
	saved, err := auth.Load(dir, "srv")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if saved.AccessToken != "t4-access" {
		t.Errorf("persisted = %q, want t4-access", saved.AccessToken)
	}
}

func TestRefreshAuthorization_externalLoginWithNewRegistration_usesNewClientCredentials(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	clk := clock.NewFake()

	if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "dcr-v1"}); err != nil {
		t.Fatal(err)
	}
	initialTok := &oauth2.Token{
		AccessToken: "initial-access", RefreshToken: "initial-refresh",
		Expiry: clk.Now().Add(time.Hour),
	}
	if err := auth.Save(dir, "srv", initialTok); err != nil {
		t.Fatal(err)
	}
	p, err := provider.New(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, TokenURL: endpoint.Srv.URL + "/token"},
		ConfigDir:  dir, ServerName: "srv", Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := auth.SaveRegistration(dir, "srv", &auth.Registration{ClientID: "dcr-v2"}); err != nil {
		t.Fatal(err)
	}
	freshTok := &oauth2.Token{
		AccessToken: "auth-access", RefreshToken: "auth-refresh",
		Expiry: clk.Now().Add(time.Hour),
	}
	if err := auth.Save(dir, "srv", freshTok); err != nil {
		t.Fatal(err)
	}

	got, err := p.RefreshAuthorization(context.Background(), "Bearer initial-access")
	if err != nil {
		t.Fatalf("RefreshAuthorization: %v", err)
	}
	if got != "Bearer auth-access" {
		t.Fatalf("expected adopted token, got %q", got)
	}
	if endpoint.Hits.Load() != 0 {
		t.Fatalf("token endpoint hit during adoption, want 0 hits")
	}

	clk.Advance(2 * time.Hour)
	if _, err := p.Authorization(context.Background()); err != nil {
		t.Fatalf("Authorization after expiry: %v", err)
	}

	endpoint.Mu.Lock()
	clientIDForm, clientIDBasic := endpoint.LastClientID, endpoint.LastBasicAuth
	endpoint.Mu.Unlock()

	if clientIDForm != "dcr-v2" && clientIDBasic != "dcr-v2" {
		t.Errorf("token endpoint client_id = form:%q basic:%q, want dcr-v2 (rehydrate must use new registration)", clientIDForm, clientIDBasic)
	}
}

func TestRemedyError_wrapsErrReauthRequired(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{})
		_, err := f.provider.Authorization(context.Background())
		if !errors.Is(err, transport.ErrReauthRequired) {
			t.Errorf("error = %v, want ErrReauthRequired", err)
		}
	})
	t.Run("refresh invalid_grant", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
		f.endpoint.RespondWith(http.StatusBadRequest, `{"error":"invalid_grant"}`)
		_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		if !errors.Is(err, transport.ErrReauthRequired) {
			t.Errorf("error = %v, want ErrReauthRequired", err)
		}
	})
}

func TestRefreshAuthorization_invalidGrant_returnsReauthRemedy(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.RespondWith(http.StatusBadRequest, `{"error":"invalid_grant"}`)
	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("invalid_grant must be ErrReauthRequired, got: %v", err)
	}
}

func TestRefreshAuthorization_noRefreshToken_returnsReauthWithoutNetwork(t *testing.T) {
	tokenWithNoRefresh := &oauth2.Token{AccessToken: "stored-access"}
	f := newProviderFixture(t, providerSetup{Token: tokenWithNoRefresh})
	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("no-refresh-token must be ErrReauthRequired, got: %v", err)
	}
	if hits := f.endpoint.Hits.Load(); hits != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (must not contact endpoint)", hits)
	}
}

func TestRefreshAuthorization_malformedSuccessResponse_isTransient(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.RespondWith(http.StatusOK, `{"token_type":"Bearer"}`)
	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected error for malformed 200 response")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Errorf("malformed 200 must NOT be ErrReauthRequired, got: %v", err)
	}
}
