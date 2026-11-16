package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mcpmini/mini/internal/response"
)

type loginRequest struct {
	Server string `json:"server"`
}

type loginURLLine struct {
	URL string `json:"url"`
}

type loginSuccessLine struct {
	OK           bool    `json:"ok"`
	Expires      *string `json:"expires"`
	ConnectError *string `json:"connect_error"`
}

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
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeLoginError(w, http.StatusBadRequest, "invalid login request: "+err.Error())
		return
	}
	flow, err := s.beginAuthFlow(r.Context(), authFlowParams{Server: req.Server, LeaveBrowserClosed: true})
	if err != nil {
		// Never 404: a bare 404 is how the terminal recognizes a daemon without this route.
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
	writeLoginLine(p.Writer, p.Flusher, loginURLLine{URL: p.Flow.login.AuthURL()})
	select {
	case <-p.Flow.done:
		writeLoginLine(p.Writer, p.Flusher, loginOutcomeLine(p.Flow.outcome))
	case <-p.Request.Context().Done():
		s.cancelUncommittedAuthFlow(p.Flow.settings.Name, p.Flow)
	}
}

func loginOutcomeLine(o loginOutcome) any {
	if o.err != nil {
		return loginErrorBody(o.err.Error())
	}
	line := loginSuccessLine{OK: true}
	if !o.expiry.IsZero() {
		expires := o.expiry.Format(time.RFC3339)
		line.Expires = &expires
	}
	if o.connectErr != nil {
		connectErr := o.connectErr.Error()
		line.ConnectError = &connectErr
	}
	return line
}

func loginErrorBody(message string) *response.Envelope {
	return response.BuildError(response.BuildErrorParams{Code: "login_failed", Message: message})
}

func writeLoginError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	//nolint:errcheck // The terminal reads a broken body as a failed login; a failed writer has no fallback.
	json.NewEncoder(w).Encode(loginErrorBody(message))
}

func writeLoginLine(w http.ResponseWriter, flusher http.Flusher, line any) {
	//nolint:errcheck // A closed connection ends the request, which cancels the login it was waiting on.
	json.NewEncoder(w).Encode(line)
	flusher.Flush()
}
