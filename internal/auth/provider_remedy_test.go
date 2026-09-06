//go:build test

package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func TestRemedyError_wrapsErrReauthRequired(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{})
		_, err := f.provider.Authorization(context.Background())
		if !errors.Is(err, transport.ErrReauthRequired) {
			t.Errorf("error = %v, want ErrReauthRequired", err)
		}
	})
	t.Run("refresh failure", func(t *testing.T) {
		f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
		f.endpoint.status.Store(http.StatusUnauthorized)
		_, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access")
		if !errors.Is(err, transport.ErrReauthRequired) {
			t.Errorf("error = %v, want ErrReauthRequired", err)
		}
	})
}
