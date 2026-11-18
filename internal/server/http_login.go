package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
)

func (s *Server) serveLogin(w http.ResponseWriter, r *http.Request) {
	if !s.admitLocalRequest(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := streamFlusher(w)
	if !ok {
		return
	}
	var req daemon.LoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeLoginError(w, http.StatusBadRequest, "invalid login request: "+err.Error())
		return
	}
	flow, err := s.beginAuthFlow(r.Context(), authFlowParams{Server: req.Server, LeaveBrowserClosed: true})
	if err != nil {
		writeLoginError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.streamLogin(loginStreamParams{Request: r, Writer: w, Flusher: flusher, Flow: flow})
}

type loginStreamParams struct {
	Request *http.Request
	Writer  http.ResponseWriter
	Flusher http.Flusher
	Flow    *authFlowState
}

func (s *Server) streamLogin(p loginStreamParams) {
	p.Writer.Header().Set("Content-Type", "application/x-ndjson")
	p.Writer.WriteHeader(http.StatusOK)
	writeLoginLine(p.Writer, p.Flusher, daemon.LoginLine{URL: p.Flow.login.AuthURL()})
	select {
	case <-p.Flow.done:
		writeLoginLine(p.Writer, p.Flusher, loginOutcomeLine(p.Flow.outcome))
	case <-p.Request.Context().Done():
	}
}

func loginOutcomeLine(o loginOutcome) daemon.LoginLine {
	if o.err != nil {
		return loginErrorLine(o.err.Error())
	}
	line := daemon.LoginLine{OK: true}
	if !o.expiry.IsZero() {
		line.Expires = o.expiry.Format(time.RFC3339)
	}
	if o.connectErr != nil {
		line.ConnectError = o.connectErr.Error()
	}
	return line
}

func loginErrorLine(message string) daemon.LoginLine {
	return daemon.LoginLine{Error: "login_failed", Message: message}
}

func writeLoginError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	//nolint:errcheck // The terminal reads a broken body as a failed login; a failed writer has no fallback.
	json.NewEncoder(w).Encode(loginErrorLine(message))
}

func writeLoginLine(w http.ResponseWriter, flusher http.Flusher, line daemon.LoginLine) {
	//nolint:errcheck // A failed write means the terminal is gone; there is no one left to tell.
	json.NewEncoder(w).Encode(line)
	flusher.Flush()
}
