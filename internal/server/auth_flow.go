package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type authFlowParams struct {
	Server             string
	LeaveBrowserClosed bool
	// An agent can't enable the server it logs in to, so its login would end with nothing connected.
	RefuseDisabled bool
}

type authFlowState struct {
	cancel  context.CancelFunc
	login   *auth.BrowserLogin
	server  string
	done    chan struct{} // closed once outcome is set, after the login's reconnect
	outcome loginOutcome
	// cancelReason and committed are guarded by Server.authMu.
	cancelReason error
	committed    bool
}

type loginOutcome struct {
	expiry     time.Time
	connectErr error
	err        error
}

var (
	errLoginReplaced     = errors.New("replaced by another login")
	errLoginSettingsEdit = errors.New("the server's settings changed")
	errLoginServerGone   = errors.New("the server was removed")
	errLoginRequestEnded = errors.New("the login request ended")
)

func (s *Server) beginAuthFlow(ctx context.Context, p authFlowParams) (*authFlowState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateServerName(p.Server); err != nil {
		return nil, err
	}
	// Held until the login is registered, so a remove_server either runs first or cancels the
	// login, and can't delete the server's files while the login is still writing them.
	unlock := s.serverNames.lock(p.Server)
	defer unlock()
	sc, err := s.loadLoginServer(ctx, p)
	if err != nil {
		return nil, err
	}
	s.applyEditBeforeLogin(sc)
	return s.launchAuthFlow(ctx, sc, p)
}

func (s *Server) loadLoginServer(ctx context.Context, p authFlowParams) (config.ServerConfig, error) {
	sc, err := s.loadOAuthServerConfig(p.Server)
	if err != nil {
		return config.ServerConfig{}, err
	}
	if p.RefuseDisabled && !sc.IsEnabled() {
		return config.ServerConfig{}, fmt.Errorf(
			`server %q is disabled; enable it, or log in with "mini auth %s"`,
			p.Server,
			p.Server,
		)
	}
	return sc, ctx.Err()
}

// applyEditBeforeLogin puts a running server on its file's settings, so the login is for them and
// only a later edit replaces the server and cancels the login; one the file now disables stops,
// and the login only saves its token. A server not running is left for the finished login to
// start: connecting it now would only fail for want of that login.
func (s *Server) applyEditBeforeLogin(sc config.ServerConfig) {
	if !sc.IsEnabled() {
		if s.removeConfigServer(sc.Name) {
			s.notifyAllSessions()
		}
		return
	}
	if !s.settingsChanged(sc) {
		return
	}
	s.logger.Info("applying the server's edited settings before its login", "server", sc.Name)
	s.replace(sc.Name, s.newConfigServer(sc))
	s.notifyAllSessions()
}

func (s *Server) launchAuthFlow(ctx context.Context, sc config.ServerConfig, p authFlowParams) (*authFlowState, error) {
	target := s.loginTargetFor(sc)
	login, err := s.startPKCEFlow(ctx, sc.Name, sc)
	if err != nil {
		return nil, err
	}
	authCtx, flow, err := s.registerAuthFlow(ctx, login, sc.Name)
	if err != nil {
		return nil, err
	}
	s.authWg.Add(1)
	go s.runAuthFlow(authCtx, target, flow)
	if !p.LeaveBrowserClosed {
		s.maybeOpenAuthBrowser(sc, login.AuthURL())
	}
	return flow, nil
}

func (s *Server) maybeOpenAuthBrowser(sc config.ServerConfig, authURL string) {
	if browserCmd, open := s.cfg.BrowserCommandFor(sc); open {
		//nolint:errcheck // Browser launch is optional; handleStartAuth returns the URL for manual use.
		_ = auth.OpenBrowser(browserCmd, authURL)
	}
}

func (s *Server) startPKCEFlow(
	ctx context.Context,
	serverName string,
	sc config.ServerConfig,
) (*auth.BrowserLogin, error) {
	s.cancelExistingAuthFlow(serverName, errLoginReplaced)
	params := auth.BeginLoginParams{ConfigDir: s.configDir, ServerName: serverName, Clock: s.clock}
	login, err := auth.BeginLogin(ctx, sc, params)
	if err != nil {
		return nil, err
	}
	return login, nil
}

func (s *Server) registerAuthFlow(
	ctx context.Context,
	login *auth.BrowserLogin,
	server string,
) (context.Context, *authFlowState, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Join(err, login.Close())
	}
	authCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	state := newAuthFlow(cancel, login, server)
	s.storeAuthFlow(server, state)
	return authCtx, state, nil
}

func newAuthFlow(cancel context.CancelFunc, login *auth.BrowserLogin, server string) *authFlowState {
	return &authFlowState{cancel: cancel, login: login, server: server, done: make(chan struct{})}
}

func (s *Server) storeAuthFlow(serverName string, state *authFlowState) {
	s.authMu.Lock()
	s.authFlows[serverName] = state
	s.authMu.Unlock()
}

func (s *Server) cancelExistingAuthFlow(serverName string, reason error) {
	s.authMu.Lock()
	old := s.authFlows[serverName]
	delete(s.authFlows, serverName)
	// A newer login can still be abandoned in the browser, so one whose token is saved finishes
	// its own reconnect.
	savedLoginKeepsReconnecting := old != nil && old.committed && errors.Is(reason, errLoginReplaced)
	cancel := old != nil && !savedLoginKeepsReconnecting
	if cancel {
		old.markCancelledLocked(reason)
	}
	s.authMu.Unlock()
	if cancel {
		old.stop()
	}
}

func (s *Server) cancelUncommittedAuthFlow(serverName string, flow *authFlowState) {
	s.authMu.Lock()
	current := s.authFlows[serverName] == flow && !flow.committed
	if current {
		flow.markCancelledLocked(errLoginRequestEnded)
		delete(s.authFlows, serverName)
	}
	s.authMu.Unlock()
	if current {
		flow.stop()
	}
}

func (f *authFlowState) markCancelledLocked(reason error) {
	if f.cancelReason == nil {
		f.cancelReason = reason
	}
}

func (f *authFlowState) stop() {
	//nolint:errcheck // Close joins the callback server and currently always returns nil.
	f.login.Close()
	f.cancel()
}
