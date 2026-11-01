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
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // Fixed JSON; failed writer has no fallback.
		"ok":       true,
		"sessions": s.sessions.count(),
	})
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeDaemon(w, r) {
		return
	}
	if !hostIsLoopback(r.Host) {
		http.Error(w, "forbidden: non-loopback Host", http.StatusForbidden)
		return
	}
	if rejectCrossOrigin(w, r) {
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.servePost(w, r)
	case http.MethodGet:
		s.serveGet(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveGet(w http.ResponseWriter, r *http.Request) {
	if !acceptsSSE(r.Header.Get("Accept")) {
		http.Error(w, "Accept must include text/event-stream", http.StatusNotAcceptable)
		return
	}
	session, ok := s.streamSession(w, r)
	if !ok {
		return
	}
	flusher, ok := streamFlusher(w)
	if !ok {
		return
	}
	s.writeNotificationStream(notificationStreamParams{
		Request: r, Writer: w, Flusher: flusher, Session: session,
	})
}

func (s *Server) streamSession(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Mcp-Session-Id is required", http.StatusBadRequest)
		return nil, false
	}
	if !sessionIDPattern.MatchString(sessionID) || nonHyphenCount(sessionID) < 16 {
		http.Error(w, "invalid Mcp-Session-Id", http.StatusBadRequest)
		return nil, false
	}
	session := s.sessions.get(sessionID)
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return nil, false
	}
	return session, true
}

func streamFlusher(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return nil, false
	}
	return flusher, true
}

type notificationStreamParams struct {
	Request *http.Request
	Writer  http.ResponseWriter
	Flusher http.Flusher
	Session *Session
}

func (s *Server) writeNotificationStream(p notificationStreamParams) {
	stream, ok := p.Session.openToolsChangedStream()
	if !ok {
		p.Writer.WriteHeader(http.StatusNoContent)
		return
	}
	defer p.Session.closeToolsChangedStream(stream)
	startEventStream(p.Writer, p.Flusher)
	s.writeNotifications(notificationParams{
		Request: p.Request, Writer: p.Writer, Flusher: p.Flusher, Stream: stream,
	})
}

func startEventStream(w http.ResponseWriter, flusher http.Flusher) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
}

type notificationParams struct {
	Request *http.Request
	Writer  http.ResponseWriter
	Flusher http.Flusher
	Stream  chan json.RawMessage
}

func (s *Server) writeNotifications(p notificationParams) {
	for {
		select {
		case <-p.Request.Context().Done():
			return
		case notification, open := <-p.Stream:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(p.Writer, "event: message\ndata: %s\n\n", notification); err != nil {
				return
			}
			p.Flusher.Flush()
		}
	}
}

type parsedPostRequest struct {
	body      []byte
	sessionID string
	session   *Session
	release   func()
}

func (s *Server) servePost(w http.ResponseWriter, r *http.Request) {
	req, ok := s.parsePostRequest(w, r)
	if !ok {
		return
	}
	defer req.release()
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
	session, release := s.sessions.acquire(sessionID)
	session.httpSession.Store(true)
	return parsedPostRequest{body: body, sessionID: sessionID, session: session, release: release}, true
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
	json.NewEncoder(w).Encode(p.Resp) //nolint:errcheck // okResponse marshals results; writer failure has no fallback.
}

func writeSSEResponse(w http.ResponseWriter, resp transport.Response) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	b, _ := json.Marshal(resp)                        //nolint:errcheck // Production Result JSON comes from okResponse.
	fmt.Fprintf(w, "event: message\ndata: %s\n\n", b) //nolint:errcheck // Failed SSE writer has no response fallback.
}

func acceptsSSE(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "text/event-stream" {
			return true
		}
	}
	return false
}
