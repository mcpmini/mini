// Package mcptest provides an in-process fake of a legacy MCP server over HTTP.
// It imports no mini package, so every package's tests can use it.
package mcptest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// LatestLegacyVersion mirrors transport.LatestLegacyVersion, which this package can't import.
const LatestLegacyVersion = "2025-11-25"

// Upstream describes the fake server. The zero value is a legacy server with no tools.
type Upstream struct {
	Tools []Tool
	// Authorize sees every JSON-RPC request; false answers 401 with WWW-Authenticate: Bearer.
	Authorize func(Request) bool
	// Methods answers methods beyond initialize and tools/list, or overrides them.
	Methods map[string]Method
	// Intercept runs before the request body is read; true means it wrote the response.
	Intercept func(http.ResponseWriter, *http.Request) bool
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type Request struct {
	Method string
	ID     json.RawMessage
	Header http.Header
}

type Method func(Request) Reply

// Reply is a JSON-RPC result or error, or with Status set, a bare HTTP response.
type Reply struct {
	Result any
	Error  *Error
	Status int
	Body   []byte
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Handler struct {
	upstream Upstream
	mu       sync.Mutex
	requests []Request
}

type Server struct {
	*httptest.Server
	*Handler
}

func NewHandler(u Upstream) *Handler {
	return &Handler{upstream: u}
}

// NewHTTP starts the fake and closes it when the test ends.
func NewHTTP(t testing.TB, u Upstream) *Server {
	t.Helper()
	h := NewHandler(u)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Server{Server: srv, Handler: h}
}

// Requests returns every JSON-RPC request received so far, rejected ones included.
func (h *Handler) Requests() []Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Request(nil), h.requests...)
}

// Count returns how many requests for method were received so far, rejected ones included.
func (h *Handler) Count(method string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, req := range h.requests {
		if req.Method == method {
			n++
		}
	}
	return n
}

// Last returns the most recent request for method, or the zero Request when none arrived.
func (h *Handler) Last(method string) Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.requests) - 1; i >= 0; i-- {
		if h.requests[i].Method == method {
			return h.requests[i]
		}
	}
	return Request{}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.upstream.Intercept != nil && h.upstream.Intercept(w, r) {
		return
	}
	req, ok := decodeRequest(w, r)
	if !ok {
		return
	}
	h.record(req)
	if h.upstream.Authorize != nil && !h.upstream.Authorize(req) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeReply(w, req.ID, h.reply(req))
}

func (h *Handler) record(req Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, req)
}

func (h *Handler) reply(req Request) Reply {
	if m, ok := h.upstream.Methods[req.Method]; ok {
		return m(req)
	}
	switch req.Method {
	case "initialize":
		return Reply{Result: h.initializeResult()}
	case "tools/list":
		return Reply{Result: toolsListResult{Tools: withDefaultSchemas(h.upstream.Tools)}}
	}
	return Reply{Error: &Error{Code: codeMethodNotFound, Message: "method not found: " + req.Method}}
}

func (h *Handler) initializeResult() initializeResult {
	return initializeResult{
		ProtocolVersion: LatestLegacyVersion,
		Capabilities:    capabilities{Tools: struct{}{}},
		ServerInfo:      serverInfo{Name: "mcptest", Version: "0"},
	}
}
