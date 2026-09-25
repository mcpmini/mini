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
	"github.com/mcpmini/mini/internal/server"
)

func TestBuildAndStartConnecting_validAndDisabledOAuthServers_makeNoTokenRequests(t *testing.T) {
	configDir := t.TempDir()
	tokenEp := newTestTokenEndpoint(t)
	mcp := newTestMCPUpstream(t)
	validTok := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}
	if err := auth.Save(configDir, "live", validTok); err != nil {
		t.Fatal(err)
	}
	expiredTok := &oauth2.Token{AccessToken: "dead-access", RefreshToken: "r2", Expiry: time.Now().Add(-time.Hour)}
	if err := auth.Save(configDir, "idle", expiredTok); err != nil {
		t.Fatal(err)
	}
	servers := []config.ServerConfig{
		oauthServerConfig("live", mcp.srv.URL, tokenEp.srv.URL, true),
		oauthServerConfig("idle", "http://localhost:1", tokenEp.srv.URL, false),
	}
	srv := buildAndStartConnecting(context.Background(),
		BuildServerParams{Cfg: &config.Config{}, ConfigDir: configDir,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Servers: servers},
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
	srv := buildAndStartConnecting(context.Background(),
		BuildServerParams{Cfg: &config.Config{}, ConfigDir: t.TempDir(),
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Servers: []config.ServerConfig{sc}},
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
		s := newOAuthTestSetup(t, tok)
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
		saved, err := auth.Load(s.configDir, "live")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if saved.AccessToken != "refreshed" {
			t.Errorf("persisted access = %q, want refreshed", saved.AccessToken)
		}
		if saved.RefreshToken != "rotated" {
			t.Errorf("persisted refresh = %q, want rotated", saved.RefreshToken)
		}
	})

	t.Run("expired_by_clock", func(t *testing.T) {
		clk := clock.NewFake()
		tok := &oauth2.Token{AccessToken: "stored-access", RefreshToken: "r1", Expiry: clk.Now().Add(time.Hour)}
		s := newOAuthTestSetup(t, tok, server.WithClock(clk))
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
