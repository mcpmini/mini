package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/config"
)

type authFlowParams struct {
	Server             string
	LeaveBrowserClosed bool
}

type authFlowState struct {
	cancel   context.CancelFunc
	login    *auth.BrowserLogin
	settings config.ServerConfig
	done     chan struct{} // closed once outcome is set, after the login's reconnect
	outcome  loginOutcome
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
	sc, err := s.loadOAuthServerConfig(p.Server)
	if err != nil {
		return nil, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	return s.launchAuthFlow(ctx, sc, p)
}

func (s *Server) launchAuthFlow(ctx context.Context, sc config.ServerConfig, p authFlowParams) (*authFlowState, error) {
	settings := withOwnAuth(sc)
	install := s.replacingInstall(sc)
	login, err := s.startPKCEFlow(ctx, sc.Name, sc)
	if err != nil {
		return nil, err
	}
	authCtx, flow, err := s.registerAuthFlow(ctx, login, settings)
	if err != nil {
		return nil, err
	}
	s.authWg.Add(1)
	go s.runAuthFlow(authCtx, install, flow)
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
	settings config.ServerConfig,
) (context.Context, *authFlowState, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Join(err, login.Close())
	}
	authCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	state := newAuthFlow(cancel, login, settings)
	s.storeAuthFlow(settings.Name, state)
	return authCtx, state, nil
}

func newAuthFlow(cancel context.CancelFunc, login *auth.BrowserLogin, settings config.ServerConfig) *authFlowState {
	return &authFlowState{cancel: cancel, login: login, settings: settings, done: make(chan struct{})}
}

func (s *Server) storeAuthFlow(serverName string, state *authFlowState) {
	s.authMu.Lock()
	s.authFlows[serverName] = state
	s.authMu.Unlock()
}

// loginInFlightFor is true while start_auth logs in with sc's settings: once it finishes, its own
// reconnect applies them.
func (s *Server) loginInFlightFor(sc config.ServerConfig) bool {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	flow := s.authFlows[sc.Name]
	return flow != nil && sameSettings(flow.settings, sc)
}

func (s *Server) hasAuthFlow(serverName string) bool {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	return s.authFlows[serverName] != nil
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

func (s *Server) runAuthFlow(ctx context.Context, install upstreamInstall, flow *authFlowState) {
	defer s.authWg.Done()
	defer flow.cancel()
	defer s.clearAuthFlow(install.cfg.Name, flow)
	flow.finish(s.awaitAuthAndReconnect(ctx, install, flow))
}

func (s *Server) clearAuthFlow(serverName string, flow *authFlowState) {
	s.authMu.Lock()
	if s.authFlows[serverName] == flow {
		delete(s.authFlows, serverName)
	}
	s.authMu.Unlock()
}

func (f *authFlowState) finish(outcome loginOutcome) {
	f.outcome = outcome
	close(f.done)
}

func (s *Server) awaitAuthAndReconnect(ctx context.Context, install upstreamInstall, flow *authFlowState) loginOutcome {
	sc := install.cfg
	savedLogin, err := flow.login.Wait(ctx)
	if err != nil {
		s.logger.Error("oauth flow failed", "server", sc.Name, "err", err)
		return loginOutcome{err: s.loginFailure(flow, fmt.Errorf("auth flow: %w", err))}
	}
	if !s.beginCommit(flow) {
		return loginOutcome{err: s.loginFailure(flow, nil)}
	}
	if err := s.commitTokenUnlessRemoved(install, savedLogin); err != nil {
		s.logger.Error("commit oauth token failed", "server", sc.Name, "err", err)
		return loginOutcome{err: fmt.Errorf("save login: %w", err)}
	}
	outcome := loginOutcome{expiry: savedLogin.Token.Expiry}
	// The reload loop never removes a server it didn't connect, so installing a disabled one
	// would leave it running.
	if sc.IsEnabled() {
		outcome.connectErr = s.connectAfterLogin(ctx, install, flow)
	}
	return outcome
}

func (s *Server) connectAfterLogin(ctx context.Context, install upstreamInstall, flow *authFlowState) error {
	err := s.reconnectWithToken(ctx, install)
	if err == nil {
		s.recordLoginSettings(install, flow.settings)
	}
	return s.connectFailure(flow, err)
}

// An edit or remove applied since the login started has its own record, which this mustn't replace.
func (s *Server) recordLoginSettings(install upstreamInstall, settings config.ServerConfig) {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if s.removeGen[install.cfg.Name] == install.removeGen {
		s.recordSettings(settings)
	}
}

// beginCommit makes the commit and cancellation exclusive: a cancelled flow never saves its token,
// and one that has begun saving it is no longer cancelled by its request ending.
func (s *Server) beginCommit(flow *authFlowState) bool {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	flow.committed = flow.cancelReason == nil
	return flow.committed
}

func (s *Server) loginFailure(flow *authFlowState, err error) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if flow.cancelReason != nil {
		return fmt.Errorf("login cancelled: %w", flow.cancelReason)
	}
	return err
}

// A settings edit or remove that cancels this login after its token is saved reconnects or drops the
// server itself, so this login's cut-short reconnect is no failure of it.
func (s *Server) connectFailure(flow *authFlowState, err error) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if flow.cancelReason != nil {
		return nil
	}
	return err
}

func (s *Server) commitTokenUnlessRemoved(install upstreamInstall, login *auth.Login) error {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	// A remove bumps the generation under serverOpMu before deleting the token, so a login that
	// finishes after it can't save the token back, or onto a new server added under the name.
	if s.removeGen[install.cfg.Name] != install.removeGen {
		return fmt.Errorf("server %q: %w", install.cfg.Name, errServerRemoved)
	}
	return s.providerRegistry.CommitAuthorizedToken(s.providerParamsFor(install.cfg), login)
}

func (s *Server) providerParamsFor(sc config.ServerConfig) provider.Params {
	return provider.Params{
		AuthConfig: sc.Auth,
		AgentAdded: sc.AgentAdded,
		ConfigDir:  s.configDir,
		ServerName: sc.Name,
		ServerURL:  sc.URL,
		Clock:      s.clock,
	}
}

func (s *Server) reconnectWithToken(ctx context.Context, install upstreamInstall) error {
	serverName := install.cfg.Name
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Do not call removeServerRuntime first: if AddUpstream fails the server
	// would be permanently gone. registerUpstream → swapUpstream replaces the
	// old upstream in-place; on failure the old upstream is untouched.
	if err := s.addUpstream(ctx, install); err != nil {
		s.logger.Error("reconnect after auth failed", "server", serverName, "err", err)
		return err
	}
	s.notifyAllSessions()
	s.logger.Info("reconnected after auth", "server", serverName)
	return nil
}
