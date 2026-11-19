package server

import (
	"context"
	"fmt"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/config"
)

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
	if sc.IsEnabled() {
		outcome.connectErr = s.connectAfterLogin(ctx, install, flow)
	}
	return outcome
}

func (s *Server) connectAfterLogin(ctx context.Context, install upstreamInstall, flow *authFlowState) error {
	return s.connectFailure(flow, s.reconnectWithToken(ctx, install))
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
	// Not AddUpstream, which retires the server first: a failed reconnect here leaves the server
	// that ran before the login in place.
	if err := s.addUpstream(ctx, install); err != nil {
		s.logger.Error("reconnect after auth failed", "server", serverName, "err", err)
		return err
	}
	s.logger.Info("reconnected after auth", "server", serverName)
	return nil
}
