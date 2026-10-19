package server

import (
	"context"
	"encoding/json"
)

// Raw IDs preserve JSON-RPC's distinction between numeric and string IDs.
func (s *Session) registerInFlight(id json.RawMessage, cancel context.CancelFunc) {
	s.inFlightMu.Lock()
	s.inFlight[string(id)] = cancel
	s.inFlightMu.Unlock()
}

func (s *Session) removeInFlight(id json.RawMessage) {
	s.inFlightMu.Lock()
	delete(s.inFlight, string(id))
	s.inFlightMu.Unlock()
}

func (s *Session) cancelInFlight(id json.RawMessage) {
	s.inFlightMu.Lock()
	cancel := s.inFlight[string(id)]
	s.inFlightMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
