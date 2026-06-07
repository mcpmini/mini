package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type authFlowState struct {
	cancel   context.CancelFunc
	listener net.Listener
}

func (s *Server) handleStartAuth(serverName string) (any, error) {
	if err := validateServerName(serverName); err != nil {
		return nil, err
	}
	sc, err := s.loadOAuthServerConfig(serverName)
	if err != nil {
		return nil, err
	}
	flow, err := s.startPKCEFlow(serverName, sc)
	if err != nil {
		return nil, err
	}
	s.authWg.Add(1)
	go s.runAuthFlow(serverName, sc, flow.state, flow.doneCh)
	s.maybeOpenAuthBrowser(sc, flow.authURL)
	return authStartResponse(serverName, flow.authURL), nil
}

func (s *Server) maybeOpenAuthBrowser(sc config.ServerConfig, authURL string) {
	if s.cfg.DisableAuthBrowserOpen {
		return
	}
	browserCmd := sc.Auth.BrowserCmd
	if browserCmd == "" {
		browserCmd = s.cfg.BrowserCommand
	}
	_ = auth.OpenBrowser(browserCmd, authURL)
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

type pkceFlowResult struct {
	authURL string
	state   *authFlowState
	doneCh  <-chan auth.PKCEResult
}

func (s *Server) startPKCEFlow(serverName string, sc config.ServerConfig) (pkceFlowResult, error) {
	s.cancelExistingAuthFlow(serverName) // synchronously releases old port if any
	authCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	listener, err := listenOnCallbackPort(authCtx)
	if err != nil {
		cancel()
		return pkceFlowResult{}, err
	}
	if err := auth.ResolveEndpoints(authCtx, s.configDir, serverName, &sc); err != nil {
		listener.Close() //nolint:errcheck
		cancel()
		return pkceFlowResult{}, fmt.Errorf("resolve oauth endpoints: %w", err)
	}
	authURL, doneCh, err := auth.StartPKCEFlowOnListener(authCtx, sc.Auth, listener)
	if err != nil {
		listener.Close() //nolint:errcheck
		cancel()
		return pkceFlowResult{}, fmt.Errorf("start auth flow: %w", err)
	}
	state := &authFlowState{cancel: cancel, listener: listener}
	s.storeAuthFlow(serverName, state)
	return pkceFlowResult{authURL: authURL, state: state, doneCh: doneCh}, nil
}

func listenOnCallbackPort(ctx context.Context) (net.Listener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", auth.LoopbackCallbackPort)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for oauth callback: %w", err)
	}
	return ln, nil
}

func (s *Server) storeAuthFlow(serverName string, state *authFlowState) {
	s.authMu.Lock()
	s.authFlows[serverName] = state
	s.authMu.Unlock()
}

func (s *Server) cancelExistingAuthFlow(serverName string) {
	s.authMu.Lock()
	old := s.authFlows[serverName]
	if old != nil {
		delete(s.authFlows, serverName)
	}
	s.authMu.Unlock()
	if old == nil {
		return
	}
	old.listener.Close() //nolint:errcheck
	old.cancel()
}

func (s *Server) runAuthFlow(serverName string, sc config.ServerConfig, flow *authFlowState, doneCh <-chan auth.PKCEResult) {
	defer s.authWg.Done()
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
	auth.ApplyBearerToken(&sc, accessToken)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Do not call removeServerRuntime first: if AddUpstream fails the server
	// would be permanently gone. registerUpstream → swapUpstream replaces the
	// old upstream in-place; on failure the old upstream is untouched.
	if err := s.AddUpstream(ctx, sc); err != nil {
		s.logger.Error("reconnect after auth failed", "server", serverName, "err", err)
	} else {
		s.notifyAllSessions()
		s.logger.Info("reconnected after auth", "server", serverName)
	}
}

func (s *Server) handleAuthStatus(serverName string) (any, error) {
	if err := validateServerName(serverName); err != nil {
		return nil, err
	}
	t, err := auth.Load(s.configDir, serverName)
	if auth.IsNotFound(err) {
		return map[string]any{"server": serverName, "authorized": false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}
	return buildAuthStatusResult(serverName, t.Valid(), t.Expiry), nil
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
	_, servers, err := config.Load(s.configDir)
	if err != nil {
		return config.ServerConfig{}, err
	}
	sc := config.FindServer(servers, serverName)
	if sc == nil {
		return config.ServerConfig{}, fmt.Errorf("server %q not found in config", serverName)
	}
	return *sc, nil
}
