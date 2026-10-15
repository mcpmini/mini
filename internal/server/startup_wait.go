package server

import (
	"context"
	"errors"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

var errStoppedWaiting = errors.New("mini is shutting down")

// Codex keeps the first tools/list it gets for the whole session, so a list built before the servers
// connect would hide their tools for good; waitForStartup holds it until no server is still connecting.
func (s *Server) waitForStartup(ctx context.Context) error {
	if _, _, connecting := s.startupWaitTarget(); !connecting {
		return nil
	}
	// Each config edit opens a new window, so repeated edits must not extend one listing past the
	// client's request timeout.
	hold, endHold := clock.WithTimeout(ctx, s.clock, startupHold)
	defer endHold()
	for {
		changed, until, connecting := s.startupWaitTarget()
		if !connecting {
			return nil
		}
		timer := s.clock.NewTimer(s.clock.Until(until))
		err := s.awaitStartupChange(hold, changed, timer.Chan())
		timer.Stop()
		if errors.Is(err, errStoppedWaiting) {
			return err
		}
		if err != nil {
			return ctx.Err() // nil when only the hold ran out
		}
	}
}

func (s *Server) awaitStartupChange(ctx context.Context, changed <-chan struct{}, windowEnd <-chan time.Time) error {
	// Closing stdin doesn't end the wait: like any in-flight request, a piped tools/list still gets
	// its answer, and an MCP client shutting down follows up with SIGTERM.
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

func (s *Server) startupWaitTarget() (changed <-chan struct{}, until time.Time, connecting bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	now := s.clock.Now()
	for name := range s.configServers {
		if s.startup.state(name, now).phase() == phaseConnecting {
			return s.startup.changed, s.startup.windowEnd(name), true
		}
	}
	return nil, time.Time{}, false
}

// ReleaseStartupHolds ends startup waits early, so an HTTP shutdown's drain doesn't wait out the hold.
func (s *Server) ReleaseStartupHolds() {
	s.stopWaitingOnce.Do(func() { close(s.stopWaiting) })
}
