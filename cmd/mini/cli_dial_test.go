//go:build test

package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/transport"
)

func TestCLIDial_changedResourceRejectsLoginBeforeSendingCredentials(t *testing.T) {
	dir, token := t.TempDir(), newTestTokenEndpoint(t)
	upstream := newTestMCPUpstream(t)
	sc := oauthServerConfig("svc", "https://old.example.com/mcp", token.srv.URL, true)
	saveOAuthToken(t, dir, sc, &oauth2.Token{AccessToken: "old-resource-access", RefreshToken: "old-refresh"})
	sc.URL = upstream.srv.URL
	conn := cliTestConnection(t, dir, sc)
	_, err := conn.Call(t.Context(), "tools/call", json.RawMessage(`{"name":"t1"}`))
	if !errors.Is(err, transport.ErrReauthRequired) {
		t.Fatalf("call error=%v, want reauthorization", err)
	}
	if _, sent := upstream.lastAuth.Load("initialize"); sent || token.hits.Load() != 0 {
		t.Fatal("mismatched login reached an upstream")
	}
}

func TestCLIDial_expiredLoginRefreshesFromStoredEndpoint(t *testing.T) {
	dir, endpoint := t.TempDir(), newTestTokenEndpoint(t)
	upstream := newTestMCPUpstream(t)
	sc := oauthServerConfig("svc", upstream.srv.URL, endpoint.srv.URL, true)
	saveOAuthToken(
		t,
		dir,
		sc,
		&oauth2.Token{
			AccessToken:  "expired",
			RefreshToken: "issuing-refresh",
			Expiry:       clock.NewFake().Now().Add(-time.Hour),
		},
	)
	sc.Auth.TokenURL = ""
	conn := cliTestConnection(t, dir, sc)
	if _, err := conn.Call(t.Context(), "tools/call", json.RawMessage(`{"name":"t1"}`)); err != nil {
		t.Fatal(err)
	}
	if got := upstream.lastAuthFor("tools/call"); got != "Bearer refreshed" {
		t.Fatalf("call authorization=%q", got)
	}
	if got, _ := endpoint.refreshToken.Load().(string); got != "issuing-refresh" || endpoint.hits.Load() != 1 {
		t.Fatalf("refresh=%q, hits=%d", got, endpoint.hits.Load())
	}
	login, err := auth.LoadLogin(dir, "svc")
	if err != nil || login.Token.RefreshToken != "rotated" {
		t.Fatalf("saved login=%v, %v", login, err)
	}
}

func TestCLIDial_staticAuthenticationWinsOverMismatchedSavedLogin(t *testing.T) {
	for _, authToken := range []bool{false, true} {
		t.Run(map[bool]string{false: "header", true: "auth.token"}[authToken], func(t *testing.T) {
			dir, endpoint := t.TempDir(), newTestTokenEndpoint(t)
			upstream := newTestMCPUpstream(t)
			sc := oauthServerConfig("svc", "https://old.example.com/mcp", endpoint.srv.URL, true)
			saveOAuthToken(t, dir, sc, &oauth2.Token{AccessToken: "old-resource-access"})
			sc.URL = upstream.srv.URL
			if authToken {
				sc.Auth.Token = "manual"
			} else {
				sc.Headers = map[string]string{"Authorization": "Bearer manual"}
			}
			conn := cliTestConnection(t, dir, sc)
			if _, err := conn.Call(t.Context(), "tools/call", json.RawMessage(`{"name":"t1"}`)); err != nil {
				t.Fatal(err)
			}
			if got := upstream.lastAuthFor("tools/call"); got != "Bearer manual" || endpoint.hits.Load() != 0 {
				t.Fatalf("authorization=%q, refresh hits=%d", got, endpoint.hits.Load())
			}
		})
	}
}

func cliTestConnection(t *testing.T, dir string, sc config.ServerConfig) transport.Connection {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn, err := dialCLI(
		t.Context(),
		invoke.DialParams{
			Logger:    logger,
			Config:    config.DefaultConfig(),
			Server:    sc,
			Clock:     clock.NewFake(),
			ConfigDir: dir,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close CLI connection: %v", err)
		}
	})
	return conn
}
