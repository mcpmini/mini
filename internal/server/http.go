package server

import (
	"context"
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
	case http.MethodGet:
		s.serveStream(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type flushWriter interface {
	http.ResponseWriter
	http.Flusher
}

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	fw, ok := w.(flushWriter)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sessionID, ok := parseSessionID(w, r)
	if !ok {
		return
	}
	session := s.sessions.getOrCreate(sessionID)
	ch := session.enableNotifications()
	defer session.disableNotifications(ch)
	writeStreamHeaders(w, sessionID)
	fw.Flush()
	ctx, cancel := s.streamContext(r.Context())
	defer cancel()
	streamNotifications(ctx, fw, ch)
}

func (s *Server) streamContext(reqCtx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(reqCtx)
	go func() {
		select {
		case <-ctx.Done():
		case <-s.streamShutdown:
			cancel() // release the stream so it can't hold up graceful shutdown
		}
	}()
	return ctx, cancel
}

func (s *Server) ShutdownStreams() {
	s.streamShutdownOnce.Do(func() { close(s.streamShutdown) })
}

func streamNotifications(ctx context.Context, fw flushWriter, ch <-chan json.RawMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			if _, err := fmt.Fprintf(fw, "event: message\ndata: %s\n\n", msg); err != nil {
				return
			}
			fw.Flush()
		}
	}
}

func writeStreamHeaders(w http.ResponseWriter, sessionID string) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Mcp-Session-Id", sessionID)
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
