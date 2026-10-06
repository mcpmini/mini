package server

import (
	"context"
	"errors"
	"time"
)

var errStoppedWaiting = errors.New("mini is shutting down")

// Codex keeps the first tools/list it gets for the whole session, so a list built before the servers
// connect would hide their tools for good; waitForStartup holds it until no server is still connecting.
func (s *Server) waitForStartup(ctx context.Context) error {
	for {
		changed, until, connecting := s.startupWaitTarget()
		if !connecting {
			return nil
		}
		timer := s.clock.NewTimer(s.clock.Until(until))
		err := s.awaitStartupChange(ctx, changed, timer.Chan())
		timer.Stop()
		if err != nil {
			return err
		}
	}
}

func (s *Server) awaitStartupChange(ctx context.Context, changed <-chan struct{}, windowEnd <-chan time.Time) error {
	select {
	case <-changed:
		return nil
	case <-windowEnd:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopWaiting:
		return errStoppedWaiting
	}
}

// Any connecting server's window end will do: waitForStartup checks again when it passes.
func (s *Server) startupWaitTarget() (changed <-chan struct{}, until time.Time, connecting bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	now := s.clock.Now()
	for name := range s.configServers {
		if s.startupStateLocked(name, now).phase == phaseConnecting {
			return s.startup.changed, s.startup.startedAt[name].Add(startupHold), true
		}
	}
	return nil, time.Time{}, false
}

// StopWaiting releases requests held for startup, so an HTTP shutdown's drain doesn't wait out the hold.
func (s *Server) StopWaiting() {
	s.stopWaitingOnce.Do(func() { close(s.stopWaiting) })
}
