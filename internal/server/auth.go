package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/config"
)

type authFlowState struct {
	cancel context.CancelFunc
	login  *auth.BrowserLogin
}

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
	install := s.replacingInstall(sc)
	login, err := s.startPKCEFlow(ctx, serverName, sc)
	if err != nil {
		return nil, err
	}
	authCtx, flow, err := s.registerAuthFlow(ctx, serverName, login)
	if err != nil {
		return nil, err
	}
	s.authWg.Add(1)
	go s.runAuthFlow(authCtx, install, flow)
	s.maybeOpenAuthBrowser(sc, login.AuthURL())
	return authStartResponse(serverName, login.AuthURL()), nil
}

func (s *Server) maybeOpenAuthBrowser(sc config.ServerConfig, authURL string) {
	if browserCmd, open := s.cfg.BrowserCommandFor(sc); open {
		//nolint:errcheck // Browser launch is optional; handleStartAuth returns the URL for manual use.
		_ = auth.OpenBrowser(browserCmd, authURL)
	}
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

func (s *Server) startPKCEFlow(
	ctx context.Context,
	serverName string,
	sc config.ServerConfig,
) (*auth.BrowserLogin, error) {
	s.cancelExistingAuthFlow(serverName)
	params := auth.BeginLoginParams{ConfigDir: s.configDir, ServerName: serverName, Clock: s.clock}
	login, err := auth.BeginLogin(ctx, &sc, params)
	if err != nil {
		return nil, err
	}
	return login, nil
}

func (s *Server) registerAuthFlow(
	ctx context.Context,
	serverName string,
	login *auth.BrowserLogin,
) (context.Context, *authFlowState, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Join(err, login.Close())
	}
	authCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	state := &authFlowState{cancel: cancel, login: login}
	s.storeAuthFlow(serverName, state)
	return authCtx, state, nil
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
	//nolint:errcheck // Close joins the callback server and currently always returns nil.
	old.login.Close()
	old.cancel()
}

func (s *Server) runAuthFlow(ctx context.Context, install upstreamInstall, flow *authFlowState) {
	defer s.authWg.Done()
	defer flow.cancel()
	defer s.clearAuthFlow(install.cfg.Name, flow)
	s.awaitAuthAndReconnect(ctx, install, flow.login)
}

func (s *Server) clearAuthFlow(serverName string, flow *authFlowState) {
	s.authMu.Lock()
	if s.authFlows[serverName] == flow {
		delete(s.authFlows, serverName)
	}
	s.authMu.Unlock()
}

func (s *Server) awaitAuthAndReconnect(ctx context.Context, install upstreamInstall, login *auth.BrowserLogin) {
	sc := install.cfg
	token, err := login.Wait(ctx)
	if err != nil {
		s.logger.Error("oauth flow failed", "server", sc.Name, "err", err)
		return
	}
	if err := s.commitTokenUnlessRemoved(install, token); err != nil {
		s.logger.Error("commit oauth token failed", "server", sc.Name, "err", err)
		return
	}
	s.reconnectWithToken(ctx, install)
}

func (s *Server) commitTokenUnlessRemoved(install upstreamInstall, token *oauth2.Token) error {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	// A remove bumps the generation under serverOpMu before deleting the token, so a login that
	// finishes after it can't save the token back, or onto a new server added under the name.
	if s.removeGen[install.cfg.Name] != install.removeGen {
		return fmt.Errorf("server %q: %w", install.cfg.Name, errServerRemoved)
	}
	return s.providerRegistry.CommitAuthorizedToken(s.providerParamsFor(install.cfg), token)
}

func (s *Server) providerParamsFor(sc config.ServerConfig) provider.Params {
	return provider.Params{
		AuthConfig: sc.Auth,
		ConfigDir:  s.configDir,
		ServerName: sc.Name,
		ServerURL:  sc.URL,
		Clock:      s.clock,
	}
}

func (s *Server) reconnectWithToken(ctx context.Context, install upstreamInstall) {
	serverName := install.cfg.Name
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Do not call removeServerRuntime first: if AddUpstream fails the server
	// would be permanently gone. registerUpstream → swapUpstream replaces the
	// old upstream in-place; on failure the old upstream is untouched.
	if err := s.addUpstream(ctx, install); err != nil {
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
	sc, err := config.LoadServer(s.configDir, serverName)
	if errors.Is(err, fs.ErrNotExist) {
		return config.ServerConfig{}, fmt.Errorf("server %q not found in config", serverName)
	}
	return sc, err
}
