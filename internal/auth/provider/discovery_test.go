//go:build test

package provider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/config"
)

func clientIDSentToTokenEndpoint(endpoint *authtest.TokenServer) string {
	endpoint.Mu.Lock()
	defer endpoint.Mu.Unlock()
	if endpoint.LastClientID != "" {
		return endpoint.LastClientID
	}
	decoded, err := url.QueryUnescape(endpoint.LastBasicAuth)
	if err != nil {
		return ""
	}
	return decoded
}

func TestRefresh_savedEndpointSurvivesChangedMetadata(t *testing.T) {
	for _, proactive := range []bool{false, true} {
		t.Run(map[bool]string{false: "401", true: "expiry"}[proactive], func(t *testing.T) {
			endpoint := authtest.NewTokenServer(t)
			var metadataHits atomic.Int32
			metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				metadataHits.Add(1)
				http.Error(w, "metadata moved", http.StatusServiceUnavailable)
			}))
			defer metadata.Close()
			dir := t.TempDir()
			tok := storedToken(time.Time{})
			if proactive {
				tok.Expiry = time.Now().Add(-time.Hour)
			}
			authtest.SaveToken(
				t,
				authtest.TokenFile{
					ConfigDir:  dir,
					ServerName: "srv",
					Resource:   metadata.URL + "/mcp",
					TokenURL:   endpoint.Srv.URL + "/token",
					Client:     auth.Client{Source: auth.ClientMetadataDocument, ID: auth.ClientMetadataURL},
					Token:      tok,
				},
			)
			p, err := provider.New(
				provider.Params{
					AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2},
					ConfigDir:  dir,
					ServerName: "srv",
					ServerURL:  metadata.URL + "/mcp",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if proactive {
				_, err = p.Authorization(context.Background())
			} else {
				_, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access")
			}
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.Hits.Load() != 1 || metadataHits.Load() != 0 {
				t.Fatalf("token hits=%d metadata hits=%d, want 1 and 0", endpoint.Hits.Load(), metadataHits.Load())
			}
			if got := clientIDSentToTokenEndpoint(endpoint); got != auth.ClientMetadataURL {
				t.Fatalf("refresh client=%q, want issuing metadata client", got)
			}
			endpoint.Mu.Lock()
			resource := endpoint.LastResource
			endpoint.Mu.Unlock()
			if resource != metadata.URL+"/mcp" {
				t.Fatalf("resource=%q, want saved resource", resource)
			}
		})
	}
}

func TestRefresh_metadataClientSurvivesExternalAdoptionAndCallbackPortChange(t *testing.T) {
	dir := t.TempDir()
	endpoint := authtest.NewTokenServer(t)
	params := provider.Params{
		AuthConfig: &config.AuthConfig{
			Type:         config.AuthTypeOAuth2,
			TokenURL:     endpoint.Srv.URL + "/token",
			CallbackPort: 7777,
		},
		ConfigDir:  dir,
		ServerName: "srv",
		ServerURL:  "https://resource.example.com/mcp",
	}
	client := auth.Client{Source: auth.ClientMetadataDocument, ID: auth.ClientMetadataURL}
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Resource:   params.ServerURL,
			TokenURL:   params.AuthConfig.TokenURL,
			Client:     client,
			Token:      storedToken(time.Time{}),
		},
	)
	p, err := provider.New(params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Authorization(context.Background()); err != nil {
		t.Fatal(err)
	}
	tok := storedToken(time.Time{})
	tok.AccessToken = "external-access"
	authtest.SaveToken(
		t,
		authtest.TokenFile{
			ConfigDir:  dir,
			ServerName: "srv",
			Resource:   params.ServerURL,
			TokenURL:   params.AuthConfig.TokenURL,
			Client:     client,
			Token:      tok,
		},
	)
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatal(err)
	}
	if endpoint.Hits.Load() != 0 {
		t.Fatal("adoption exchanged a token")
	}
	if _, err = p.RefreshAuthorization(context.Background(), "Bearer external-access"); err != nil {
		t.Fatal(err)
	}
	if got := clientIDSentToTokenEndpoint(endpoint); got != auth.ClientMetadataURL {
		t.Fatalf("refresh client=%q, want metadata client", got)
	}
}

func TestRefresh_savedConfiguredClientDoesNotSwitchToMetadataClient(t *testing.T) {
	f := newProviderFixture(t, providerSetup{Token: storedToken(time.Time{})})
	if _, err := f.provider.RefreshAuthorization(context.Background(), "Bearer stored-access"); err != nil {
		t.Fatal(err)
	}
	if got := clientIDSentToTokenEndpoint(f.endpoint); got != "cid" {
		t.Fatalf("refresh client=%q, want saved configured client", got)
	}
}
