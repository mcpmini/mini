//go:build test

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func TestInjectToken_expiredToken_refreshSendsCanonicalResource(t *testing.T) {
	var capturedResourceValues []string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		capturedResourceValues = r.Form["resource"]
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "refreshed", "refresh_token": "new-refresh",
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)

	dir := t.TempDir()
	sc := &config.ServerConfig{
		Name: "srv",
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "client",
			TokenURL: tokenSrv.URL,
		},
		URL: "HTTPS://Example.COM:443/mcp",
	}
	expired := &oauth2.Token{
		AccessToken:  "expired",
		RefreshToken: "old-refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}
	if err := auth.Save(dir, "srv", expired); err != nil {
		t.Fatalf("Save: %v", err)
	}

	injectToken(context.Background(), dir, sc)

	const wantResource = "https://example.com/mcp"
	if len(capturedResourceValues) != 1 || capturedResourceValues[0] != wantResource {
		t.Errorf("resource values = %q, want [%q]", capturedResourceValues, wantResource)
	}
	if sc.Headers["Authorization"] == "" {
		t.Error("expected Authorization header to be set after refresh")
	}
}
