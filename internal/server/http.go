package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/mcpmini/mini/internal/transport"
)

// sessionIDPattern accepts UUIDs and similar hex strings: 32–128 chars, hex + hyphens.
// Minimum 32 chars prevents trivially enumerable IDs. Hex count >= 16 ensures
// actual entropy (all-hyphen strings would pass length but fail the entropy check).
var sessionIDPattern = regexp.MustCompile(`^[a-f0-9-]{32,128}$`)

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
	if origin := r.Header.Get("Origin"); origin != "" && !isSameHost(r, origin) {
		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
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

func readLimitedBody(w http.ResponseWriter, body io.Reader) ([]byte, bool) {
	data, err := io.ReadAll(io.LimitReader(body, 1<<20+1))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return nil, false
	}
	if len(data) > 1<<20 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(errorResponse(nil, transport.CodeInvalidParams, "request body exceeds 1MB limit")) //nolint:errcheck
		return nil, false
	}
	return data, true
}

func parseSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		return transport.NewSessionID(), true
	}
	if !sessionIDPattern.MatchString(id) || nonHyphenCount(id) < 16 {
		http.Error(w, "invalid Mcp-Session-Id", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

func nonHyphenCount(s string) int {
	n := 0
	for i := range len(s) {
		if s[i] != '-' {
			n++
		}
	}
	return n
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

// isSameHost blocks DNS-rebinding attacks: a malicious page can't reach the local daemon
// by redirecting to it, because the browser will set Origin to the attacker's domain.
// We do NOT fall back to X-Forwarded-Host: that header is attacker-controllable and would
// defeat the protection. If r.Host is empty, the check fails conservatively.
func isSameHost(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return r.Host != "" && u.Host == r.Host
}
