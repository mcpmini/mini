package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) handleStartAuth(ctx context.Context, serverName string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateServerName(serverName); err != nil {
		return nil, err
	}
	// Held until the login is registered, so a remove_server either runs first or cancels the
	// login, and can't delete the server's files while the login is still writing them.
	unlock := s.serverNames.lock(serverName)
	defer unlock()
	sc, err := s.loadOAuthServerConfig(serverName)
	if err != nil {
		return nil, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	settings := withOwnAuth(sc)
	install := s.replacingInstall(sc)
	login, err := s.startPKCEFlow(ctx, serverName, sc)
	if err != nil {
		return nil, err
	}
	authCtx, flow, err := s.registerAuthFlow(ctx, login, settings)
	if err != nil {
		return nil, err
	}
	s.authWg.Add(1)
	go s.runAuthFlow(authCtx, install, flow)
	s.maybeOpenAuthBrowser(sc, login.AuthURL())
	return authStartResponse(serverName, login.AuthURL()), nil
}

func (s *Server) loadOAuthServerConfig(serverName string) (config.ServerConfig, error) {
	sc, err := s.loadServerConfig(serverName)
	if err != nil {
		return config.ServerConfig{}, fmt.Errorf("load server config: %w", err)
	}
	return sc, auth.ValidateOAuthServer(serverName, sc)
}

func authStartResponse(serverName, authURL string) map[string]any {
	return map[string]any{
		"ok":   true,
		"url":  authURL,
		"note": "Visit the URL in a browser to authorize " + serverName + ". The connection will be re-established automatically once authorized.",
	}
}

func (s *Server) handleAuthStatus(serverName string) (any, error) {
	if err := validateServerName(serverName); err != nil {
		return nil, err
	}
	sc, err := s.loadOAuthServerConfig(serverName)
	if err != nil {
		return nil, err
	}
	login, err := auth.LoadLoginFor(s.configDir, sc)
	if auth.IsNotFound(err) {
		return map[string]any{"server": serverName, "authorized": false}, nil
	}
	if err != nil {
		return unauthorizedStatus(serverName, transport.ReauthorizationError(serverName, err)), nil
	}
	return buildAuthStatusResult(serverName, login.Token.Valid(), login.Token.Expiry), nil
}

func unauthorizedStatus(serverName string, reason error) map[string]any {
	return map[string]any{"server": serverName, "authorized": false, "reason": reason.Error()}
}

func buildAuthStatusResult(serverName string, valid bool, expiry time.Time) map[string]any {
	result := map[string]any{"server": serverName, "authorized": valid}
	appendTokenExpiry(result, expiry)
	return result
}

func appendTokenExpiry(result map[string]any, expiry time.Time) {
	if !expiry.IsZero() {
		result["expires"] = expiry.Format(time.RFC3339)
	}
}

func (s *Server) loadServerConfig(serverName string) (config.ServerConfig, error) {
	sc, err := config.LoadServer(s.configDir, serverName)
	if errors.Is(err, fs.ErrNotExist) {
		return config.ServerConfig{}, fmt.Errorf("server %q not found in config", serverName)
	}
	return sc, err
}
