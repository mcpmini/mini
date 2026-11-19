package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/config"
)

type loginTarget struct {
	cfg       config.ServerConfig
	removeGen uint64
}

func (s *Server) loginTargetFor(sc config.ServerConfig) loginTarget {
	return loginTarget{cfg: sc, removeGen: s.snapshotRemoveGen(sc.Name)}
}

var errServerRemoved = errors.New("removed or replaced meanwhile")

func (s *Server) runAuthFlow(ctx context.Context, target loginTarget, flow *authFlowState) {
	defer s.authWg.Done()
	defer flow.cancel()
	defer s.clearAuthFlow(target.cfg.Name, flow)
	flow.finish(s.awaitAuthAndReplace(ctx, target, flow))
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

func (s *Server) awaitAuthAndReplace(ctx context.Context, target loginTarget, flow *authFlowState) loginOutcome {
	name := target.cfg.Name
	savedLogin, err := flow.login.Wait(ctx)
	if err != nil {
		s.logger.Error("oauth flow failed", "server", name, "err", err)
		return loginOutcome{err: s.loginFailure(flow, fmt.Errorf("auth flow: %w", err))}
	}
	if !s.beginCommit(flow) {
		return loginOutcome{err: s.loginFailure(flow, nil)}
	}
	opening, err := s.commitAndReplace(target, flow, savedLogin)
	if err != nil {
		s.logger.Error("commit oauth token failed", "server", name, "err", err)
		return loginOutcome{err: fmt.Errorf("save login: %w", err)}
	}
	outcome := loginOutcome{expiry: savedLogin.Token.Expiry}
	if opening != nil {
		outcome.connectErr = opening.wait()
	}
	return outcome
}

// commitAndReplace holds the name's lock across the commit and the replace, so the watcher can't
// see the new token and replace the server in between, and a remove either runs first or waits for
// both. It returns the new incarnation's first connect, which is waited for without the lock.
func (s *Server) commitAndReplace(target loginTarget, flow *authFlowState, login *auth.Login) (*firstOpen, error) {
	name := target.cfg.Name
	unlock := s.serverNames.lock(name)
	defer unlock()
	if err := s.commitTokenUnlessRemoved(target, login); err != nil {
		return nil, err
	}
	// Cleared first: this login has saved its token, so the replace mustn't record it as cancelled.
	s.clearAuthFlow(name, flow)
	return s.replaceAfterLogin(name), nil
}

// replaceAfterLogin starts the server afresh on the file's settings, so the new incarnation's
// connect loads the saved token into a fresh provider. A disabled server keeps only the token.
func (s *Server) replaceAfterLogin(name string) *firstOpen {
	sc, saved := s.lookupSavedServer(name)
	switch saved {
	case savedGone:
		if s.removeConfigServer(name) {
			s.notifyAllSessions()
		}
		return nil
	case savedUnreadable:
		return nil
	case savedStartable:
	}
	opening := s.replaceReportingFirstOpen(name, s.newConfigServer(sc))
	s.notifyAllSessions()
	s.logger.Info("reconnecting after auth", "server", name)
	return opening
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

func (s *Server) commitTokenUnlessRemoved(target loginTarget, login *auth.Login) error {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	// Every retire bumps the generation before deleting anything, so a login that finishes after a
	// remove or an edit can't save its token back, or onto a new server added under the name.
	if s.removeGen[target.cfg.Name] != target.removeGen {
		return fmt.Errorf("server %q: %w", target.cfg.Name, errServerRemoved)
	}
	return s.providerRegistry.CommitAuthorizedToken(s.providerParamsFor(target.cfg), login)
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
