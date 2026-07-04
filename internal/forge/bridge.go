package forge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/randutil"
)

// ToolBridge allows Deno sandbox code to call mini's upstream MCP tools.
type ToolBridge interface {
	ListTools(ctx context.Context) (any, error)
	CallTool(ctx context.Context, server, tool string, params map[string]any) (json.RawMessage, error)
}

type toolBridgeServer struct {
	srv   *http.Server
	token string
	addr  string
}

func startToolBridge(ctx context.Context, tools ToolBridge) (*toolBridgeServer, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := &toolBridgeServer{
		token: randutil.HexString(16),
		addr:  ln.Addr().String(),
	}
	b.srv = &http.Server{
		Handler:           b.handler(tools),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() { _ = b.srv.Serve(ln) }()
	return b, nil
}

func (b *toolBridgeServer) handler(tools ToolBridge) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b.checkAuth(w, r) {
			return
		}
		switch r.URL.Path {
		case "/list":
			b.serveList(w, r, tools)
		case "/call":
			b.serveCall(w, r, tools)
		default:
			bridgeWriteJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	})
}

func (b *toolBridgeServer) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(b.token)) != 1 {
		bridgeWriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

func (b *toolBridgeServer) serveList(w http.ResponseWriter, r *http.Request, tools ToolBridge) {
	result, err := tools.ListTools(r.Context())
	if err != nil {
		bridgeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	bridgeWriteJSON(w, http.StatusOK, result)
}

func (b *toolBridgeServer) serveCall(w http.ResponseWriter, r *http.Request, tools ToolBridge) {
	var req struct {
		Server string         `json:"server"`
		Tool   string         `json:"tool"`
		Params map[string]any `json:"params"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bridgeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	raw, err := tools.CallTool(r.Context(), req.Server, req.Tool, req.Params)
	if err != nil {
		bridgeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	bridgeWriteRaw(w, raw)
}

func bridgeWriteRaw(w http.ResponseWriter, raw json.RawMessage) {
	if !json.Valid(raw) {
		raw, _ = json.Marshal(string(raw))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func bridgeWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (b *toolBridgeServer) hostPort() string { return b.addr }
func (b *toolBridgeServer) close()           { _ = b.srv.Close() }
