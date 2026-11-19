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
	flow, err := s.beginAuthFlow(ctx, authFlowParams{Server: serverName, RefuseDisabled: true})
	if err != nil {
		return nil, err
	}
	return authStartResponse(serverName, flow.login.AuthURL()), nil
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
