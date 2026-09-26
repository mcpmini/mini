//go:build test

package provider_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

func TestRefreshAuthorization_deadRefreshToken_notResentUntilTokenChanges(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.RespondWith(http.StatusBadRequest, `{"error":"invalid_grant"}`)

	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("first call: want ErrReauthRequired, got: %v", err)
	}
	hitsAfterFirst := f.endpoint.Hits.Load()
	if hitsAfterFirst == 0 {
		t.Fatal("first call must reach the token endpoint")
	}

	for range 3 {
		_, err = f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		if !errors.Is(err, transport.ErrReauthRequired) || !strings.Contains(err.Error(), "invalid_grant") {
			t.Fatalf("subsequent call: want the original reauth error, got: %v", err)
		}
	}
	if hits := f.endpoint.Hits.Load(); hits != hitsAfterFirst {
		t.Errorf("endpoint hits after subsequent calls = %d, want %d: a dead refresh token must not be re-sent", hits, hitsAfterFirst)
	}
}

func TestRefreshAuthorization_transient503_doesNotBlockRetry(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.Status.Store(http.StatusServiceUnavailable)

	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected transient error")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("503 must not be classified as needing re-auth: %v", err)
	}
	hitsAfterFirst := f.endpoint.Hits.Load()

	_, err = f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err == nil {
		t.Fatal("expected transient error on second call")
	}
	if errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("503 must not be classified as needing re-auth: %v", err)
	}
	if hits := f.endpoint.Hits.Load(); hits <= hitsAfterFirst {
		t.Errorf("endpoint hits = %d, want > %d: transient failure must not block retries", hits, hitsAfterFirst)
	}
}

func TestRefreshAuthorization_newTokenFromMiniAuth_clearsDeadRefreshBlock(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.RespondWith(http.StatusBadRequest, `{"error":"invalid_grant"}`)

	_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("first call: want ErrReauthRequired, got: %v", err)
	}
	hitsAfterDead := f.endpoint.Hits.Load()

	fresh := &oauth2.Token{AccessToken: "fresh-access", RefreshToken: "fresh-refresh"}
	if err := auth.Save(f.dir, "srv", fresh); err != nil {
		t.Fatal(err)
	}
	f.endpoint.ClearOverride()

	got, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
	if err != nil {
		t.Fatalf("adoption call: %v", err)
	}
	if got != "Bearer fresh-access" {
		t.Errorf("adoption call = %q, want Bearer fresh-access", got)
	}

	got, err = f.provider.RefreshAuthorization(context.Background(), "Bearer fresh-access")
	if err != nil {
		t.Fatalf("refresh after adoption: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("refresh after adoption = %q, want Bearer new-access", got)
	}
	if hits := f.endpoint.Hits.Load(); hits <= hitsAfterDead {
		t.Errorf("endpoint hits = %d, want > %d (new refresh token must not be blocked)", hits, hitsAfterDead)
	}
	f.endpoint.Mu.Lock()
	defer f.endpoint.Mu.Unlock()
	if f.endpoint.LastRefresh != "fresh-refresh" {
		t.Errorf("refresh token sent = %q, want the newly stored fresh-refresh", f.endpoint.LastRefresh)
	}
}

func TestRefreshAuthorization_reauthReturnsSameRefreshToken_clearsDeadRefreshBlock(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	f.endpoint.RespondWith(http.StatusBadRequest, `{"error":"invalid_client"}`)
	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access"); !errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("first call: want ErrReauthRequired, got: %v", err)
	}
	sameRefresh := &oauth2.Token{AccessToken: "fresh-access", RefreshToken: "stored-refresh"}
	if err := auth.Save(f.dir, "srv", sameRefresh); err != nil {
		t.Fatal(err)
	}
	f.endpoint.ClearOverride()
	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatalf("adoption call: %v", err)
	}
	got, err := f.provider.RefreshAuthorization(context.Background(), "Bearer fresh-access")
	if err != nil {
		t.Fatalf("refresh after re-auth with the same refresh token: %v", err)
	}
	if got != "Bearer new-access" {
		t.Errorf("refresh = %q, want Bearer new-access", got)
	}
}

func TestAuthorization_expiredTokenWithDeadRefreshToken_notResent(t *testing.T) {
	epoch := clock.NewFake().Now()
	f := newProviderFixture(t, providerSetup{Token: storedToken(epoch.Add(-time.Minute))})
	f.endpoint.RespondWith(http.StatusBadRequest, `{"error":"invalid_grant"}`)
	if _, err := f.provider.Authorization(context.Background()); !errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("first call: want ErrReauthRequired, got: %v", err)
	}
	hitsAfterFirst := f.endpoint.Hits.Load()
	for range 3 {
		if _, err := f.provider.Authorization(context.Background()); !errors.Is(err, transport.ErrReauthRequired) {
			t.Fatalf("subsequent call: want ErrReauthRequired, got: %v", err)
		}
	}
	if hits := f.endpoint.Hits.Load(); hits != hitsAfterFirst {
		t.Errorf("endpoint hits = %d, want %d: an expired token must not re-send a dead refresh token", hits, hitsAfterFirst)
	}
}
