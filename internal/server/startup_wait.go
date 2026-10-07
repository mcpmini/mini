package server

import (
	"context"
	"errors"
	"time"
)

var (
	errStoppedWaiting = errors.New("mini is shutting down")
	errSessionEnded   = errors.New("the session ended")
)

// Codex keeps the first tools/list it gets for the whole session, so a list built before the servers
// connect would hide their tools for good; waitForStartup holds it until no server is still connecting.
func (s *Server) waitForStartup(ctx context.Context, sessionEnded <-chan struct{}) error {
	for {
		changed, until, connecting := s.startupWaitTarget()
		if !connecting {
			return nil
		}
		timer := s.clock.NewTimer(s.clock.Until(until))
		err := s.awaitStartupChange(
			startupWaitSignals{ctx: ctx, changed: changed, windowEnd: timer.Chan(), sessionEnded: sessionEnded},
		)
		timer.Stop()
		if err != nil {
			return err
		}
	}
}

type startupWaitSignals struct {
	ctx          context.Context
	changed      <-chan struct{}
	windowEnd    <-chan time.Time
	sessionEnded <-chan struct{}
}

func (s *Server) awaitStartupChange(w startupWaitSignals) error {
	select {
	case <-w.changed:
		return nil
	case <-w.windowEnd:
		return nil
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-w.sessionEnded:
		return errSessionEnded
	case <-s.stopWaiting:
		return errStoppedWaiting
	}
}

func (s *Server) startupWaitTarget() (changed <-chan struct{}, until time.Time, connecting bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	now := s.clock.Now()
	for name := range s.configServers {
		if s.startup.state(name, now).phase == phaseConnecting {
			return s.startup.changed, s.startup.windowEnd(name), true
		}
	}
	return nil, time.Time{}, false
}

// ReleaseStartupHolds ends startup waits early, so an HTTP shutdown's drain doesn't wait out the hold.
func (s *Server) ReleaseStartupHolds() {
	s.stopWaitingOnce.Do(func() { close(s.stopWaiting) })
}
