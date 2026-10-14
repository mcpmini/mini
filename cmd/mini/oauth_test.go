//go:build test

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestBuildAndStartConnecting_validAndDisabledOAuthServers_makeNoTokenRequests(t *testing.T) {
	configDir := t.TempDir()
	tokenEp := newTestTokenEndpoint(t)
	mcp := newTestMCPUpstream(t)
	validTok := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}
	live := oauthServerConfig("live", mcp.srv.URL, tokenEp.srv.URL, true)
	saveOAuthToken(t, configDir, live, validTok)
	expiredTok := &oauth2.Token{AccessToken: "dead-access", RefreshToken: "r2", Expiry: time.Now().Add(-time.Hour)}
	idle := oauthServerConfig("idle", "http://localhost:1", tokenEp.srv.URL, false)
	saveOAuthToken(t, configDir, idle, expiredTok)
	servers := []config.ServerConfig{
		live, idle,
	}
	srv := startFromConfigDir(t,
		BuildServerParams{
			Cfg: &config.Config{}, ConfigDir: configDir,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Servers: servers,
		},
	)
	defer srv.Close()
	awaitConnected(t, srv, "live")
	if got := mcp.lastAuthFor("tools/list"); got != "Bearer stored-access" {
		t.Errorf("upstream Authorization = %v, want stored token via provider", got)
	}
	if tokenEp.hits.Load() != 0 {
		t.Errorf("token endpoint hits at startup = %d, want 0", tokenEp.hits.Load())
	}
}

func TestBuildAndStartConnecting_oauthServerWithHandSetHeaderAndNoToken_usesHandSetHeader(t *testing.T) {
	mcp := newTestMCPUpstream(t)
	sc := oauthServerConfig("pat", mcp.srv.URL, "http://localhost:1/token", true)
	sc.Headers = map[string]string{"Authorization": "Bearer pat-123"}
	srv := startFromConfigDir(t,
		BuildServerParams{
			Cfg: &config.Config{}, ConfigDir: t.TempDir(),
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Servers: []config.ServerConfig{sc},
		},
	)
	defer srv.Close()
	awaitConnected(t, srv, "pat")
	if got := mcp.lastAuthFor("tools/list"); got != "Bearer pat-123" {
		t.Errorf("upstream Authorization = %v, want the hand-set header", got)
	}
}

func TestServe_tokenExpiresMidSession_toolCallRefreshesAndSucceeds(t *testing.T) {
	t.Run("rejected_with_401", func(t *testing.T) {
		tok := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}
		s := newOAuthTestSetup(t, tok, nil)
		s.upstream.rejectWith401.Store("Bearer stored-access")
		resp := serveSingleProxyCall(t, s.srv, "live__t1")
		assertToolCallOK(t, resp)
		if got := s.token.hits.Load(); got != 1 {
			t.Errorf("token endpoint hits = %d, want 1", got)
		}
		if got, _ := s.token.refreshToken.Load().(string); got != "r1" {
			t.Errorf("refresh_token sent = %q, want r1", got)
		}
		if got := s.upstream.lastAuthFor("tools/call"); got != "Bearer refreshed" {
			t.Errorf("tools/call Authorization = %q, want Bearer refreshed", got)
		}
		saved, err := auth.LoadLogin(s.configDir, "live")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if saved.Token.AccessToken != "refreshed" {
			t.Errorf("persisted access = %q, want refreshed", saved.Token.AccessToken)
		}
		if saved.Token.RefreshToken != "rotated" {
			t.Errorf("persisted refresh = %q, want rotated", saved.Token.RefreshToken)
		}
	})

	t.Run("expired_by_clock", func(t *testing.T) {
		clk := clock.NewFake()
		tok := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r1", Expiry: clk.Now().Add(time.Hour)}
		s := newOAuthTestSetup(t, tok, clk)
		if s.token.hits.Load() != 0 {
			t.Fatal("token endpoint was hit during connect with a still-valid token")
		}
		clk.Advance(2 * time.Hour)
		resp := serveSingleProxyCall(t, s.srv, "live__t1")
		assertToolCallOK(t, resp)
		if got := s.token.hits.Load(); got != 1 {
			t.Errorf("token endpoint hits = %d, want 1 (proactive refresh on expired token)", got)
		}
		if got := s.upstream.lastAuthFor("tools/call"); got != "Bearer refreshed" {
			t.Errorf("tools/call Authorization = %q, want Bearer refreshed", got)
		}
	})
}

func TestRefreshAndSaveToken_presentsTheRefreshTokenAnotherProcessRotatedTo(t *testing.T) {
	configDir, tokenEp := t.TempDir(), newTestTokenEndpoint(t)
	sc := oauthServerConfig("live", "http://localhost:1/mcp", tokenEp.srv.URL, true)
	expired := time.Now().Add(-time.Hour)
	rotated := &oauth2.Token{AccessToken: "a2", RefreshToken: "r2", Expiry: expired}
	saveOAuthToken(t, configDir, sc, rotated)
	loadedBeforeTheRotation := &auth.Login{
		Resource: sc.URL,
		TokenURL: sc.Auth.TokenURL,
		Client:   auth.Client{Source: auth.ClientConfigured, ID: sc.Auth.ClientID},
		Token:    &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: expired},
	}

	if _, err := refreshAndSaveToken(context.Background(), configDir, &sc, loadedBeforeTheRotation); err != nil {
		t.Fatalf("refreshAndSaveToken: %v", err)
	}
	if got, _ := tokenEp.refreshToken.Load().(string); got != "r2" {
		t.Errorf("refresh_token sent = %q, want r2: r1 was already rotated away", got)
	}
}

func TestRefreshAndSaveToken_usesTheTokenAnotherProcessAlreadyRefreshed(t *testing.T) {
	configDir, tokenEp := t.TempDir(), newTestTokenEndpoint(t)
	sc := oauthServerConfig("live", "http://localhost:1/mcp", tokenEp.srv.URL, true)
	refreshed := &oauth2.Token{AccessToken: "a2", RefreshToken: "r2", Expiry: time.Now().Add(time.Hour)}
	saveOAuthToken(t, configDir, sc, refreshed)
	loadedBeforeTheRefresh := &auth.Login{
		Resource: sc.URL,
		TokenURL: sc.Auth.TokenURL,
		Client:   auth.Client{Source: auth.ClientConfigured, ID: sc.Auth.ClientID},
		Token:    &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(-time.Hour)},
	}

	got, err := refreshAndSaveToken(context.Background(), configDir, &sc, loadedBeforeTheRefresh)
	if err != nil {
		t.Fatalf("refreshAndSaveToken: %v", err)
	}
	if got.Token.AccessToken != "a2" || tokenEp.hits.Load() != 0 {
		t.Errorf("got %q after %d token requests; want a2 with none", got.Token.AccessToken, tokenEp.hits.Load())
	}
}
