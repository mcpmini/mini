package server

import (
	"context"
	"fmt"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type authFlowState struct {
	cancel context.CancelFunc
}

func (s *Server) handleStartAuth(serverName string) (any, error) {
	if !config.ValidServerName.MatchString(serverName) {
		return nil, fmt.Errorf("invalid server name: %q", serverName)
	}
	sc, err := s.loadServerConfig(serverName)
	if err != nil {
		return nil, fmt.Errorf("load server config: %w", err)
	}
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		return nil, fmt.Errorf("server %q does not have oauth2 auth configured", serverName)
	}
	flow, err := s.startPKCEFlow(serverName, sc)
	if err != nil {
		return nil, err
	}
	go s.runAuthFlow(serverName, sc, flow.state, flow.doneCh)
	return map[string]any{
		"ok":   true,
		"url":  flow.authURL,
		"note": "Visit the URL in a browser to authorize " + serverName + ". The connection will be re-established automatically once authorized.",
	}, nil
}

type pkceFlowResult struct {
	authURL string
	state   *authFlowState
	doneCh  <-chan auth.PKCEResult
}

func (s *Server) startPKCEFlow(serverName string, sc config.ServerConfig) (pkceFlowResult, error) {
	s.cancelExistingAuthFlow(serverName)
	authCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	authURL, doneCh, err := auth.StartPKCEFlow(authCtx, sc.Auth)
	if err != nil {
		cancel()
		return pkceFlowResult{}, fmt.Errorf("start auth flow: %w", err)
	}
	state := &authFlowState{cancel: cancel}
	s.authMu.Lock()
	s.authFlows[serverName] = state
	s.authMu.Unlock()
	return pkceFlowResult{authURL: authURL, state: state, doneCh: doneCh}, nil
}

func (s *Server) cancelExistingAuthFlow(serverName string) {
	s.authMu.Lock()
	old := s.authFlows[serverName]
	if old != nil {
		delete(s.authFlows, serverName)
	}
	s.authMu.Unlock()
	if old != nil {
		old.cancel()
	}
}

func (s *Server) runAuthFlow(serverName string, sc config.ServerConfig, flow *authFlowState, doneCh <-chan auth.PKCEResult) {
	defer flow.cancel()
	defer s.clearAuthFlow(serverName, flow)
	s.awaitAuthAndReconnect(serverName, sc, doneCh)
}

func (s *Server) clearAuthFlow(serverName string, flow *authFlowState) {
	s.authMu.Lock()
	if s.authFlows[serverName] == flow {
		delete(s.authFlows, serverName)
	}
	s.authMu.Unlock()
}

func (s *Server) awaitAuthAndReconnect(serverName string, sc config.ServerConfig, doneCh <-chan auth.PKCEResult) {
	result := <-doneCh
	if result.Err != nil {
		s.logger.Error("oauth flow failed", "server", serverName, "err", result.Err)
		return
	}
	if err := auth.Save(s.configDir, serverName, result.Token); err != nil {
		s.logger.Error("save token failed", "server", serverName, "err", err)
		return
	}
	s.reconnectWithToken(serverName, sc, result.Token.AccessToken)
}

func (s *Server) reconnectWithToken(serverName string, sc config.ServerConfig, accessToken string) {
	headerName := sc.Auth.Header
	if headerName == "" {
		headerName = "Authorization"
	}
	if sc.Headers == nil {
		sc.Headers = make(map[string]string)
	}
	sc.Headers[headerName] = "Bearer " + accessToken

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.removeServerRuntime(serverName) //nolint:errcheck
	if err := s.AddUpstream(ctx, sc); err != nil {
		s.logger.Error("reconnect after auth failed", "server", serverName, "err", err)
	} else {
		s.logger.Info("reconnected after auth", "server", serverName)
	}
}

func (s *Server) handleAuthStatus(serverName string) (any, error) {
	if !config.ValidServerName.MatchString(serverName) {
		return nil, fmt.Errorf("invalid server name: %q", serverName)
	}
	t, err := auth.Load(s.configDir, serverName)
	if auth.IsNotFound(err) {
		return map[string]any{"server": serverName, "authorized": false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}
	result := map[string]any{
		"server":     serverName,
		"authorized": t.Valid(),
	}
	if !t.Expiry.IsZero() {
		result["expires"] = t.Expiry.Format(time.RFC3339)
	}
	return result, nil
}

func (s *Server) loadServerConfig(serverName string) (config.ServerConfig, error) {
	_, servers, err := config.Load(s.configDir)
	if err != nil {
		return config.ServerConfig{}, err
	}
	for _, sc := range servers {
		if sc.Name == serverName {
			return sc, nil
		}
	}
	return config.ServerConfig{}, fmt.Errorf("server %q not found in config", serverName)
}
