package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		s.serveHealthz(w)
	case "/mcp":
		s.serveMCP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveHealthz(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"ok":       true,
		"sessions": s.sessions.count(),
	})
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeDaemon(w, r) {
		return
	}
	if !s.allowNonLoopbackHost && !hostIsLoopback(r.Host) {
		http.Error(w, "forbidden: non-loopback Host", http.StatusForbidden)
		return
	}
	if rejectCrossOrigin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.servePost(w, r)
	default:
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type parsedPostRequest struct {
	body      []byte
	sessionID string
	session   *Session
}

func (s *Server) servePost(w http.ResponseWriter, r *http.Request) {
	req, ok := s.parsePostRequest(w, r)
	if !ok {
		return
	}
	resp, send := s.handleLineCancellable(r.Context(), req.body, req.session)
	writeMCPResponse(w, r, mcpResponseParams{SessionID: req.sessionID, Resp: resp, Send: send})
}

func (s *Server) parsePostRequest(w http.ResponseWriter, r *http.Request) (parsedPostRequest, bool) {
	body, ok := readLimitedBody(w, r.Body)
	if !ok {
		return parsedPostRequest{}, false
	}
	sessionID, ok := parseSessionID(w, r)
	if !ok {
		return parsedPostRequest{}, false
	}
	return parsedPostRequest{body, sessionID, s.sessions.getOrCreate(sessionID)}, true
}

type mcpResponseParams struct {
	SessionID string
	Resp      transport.Response
	Send      bool
}

func writeMCPResponse(w http.ResponseWriter, r *http.Request, p mcpResponseParams) {
	w.Header().Set("Mcp-Session-Id", p.SessionID)
	if !p.Send {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if acceptsSSE(r.Header.Get("Accept")) {
		writeSSEResponse(w, p.Resp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p.Resp) //nolint:errcheck
}

func writeSSEResponse(w http.ResponseWriter, resp transport.Response) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	b, _ := json.Marshal(resp)
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", b) //nolint:errcheck
}

func acceptsSSE(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "text/event-stream" {
			return true
		}
	}
	return false
}
