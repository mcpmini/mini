package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// connError wraps transport-level errors so callers can distinguish them from
// tool-level errors and trigger reconnect logic.
type connError struct{ err error }

func (e connError) Error() string { return e.err.Error() }
func (e connError) Unwrap() error { return e.err }

func isConnError(err error) bool {
	var ce connError
	return errors.As(err, &ce)
}

type upstreamServer struct {
	cfg    config.ServerConfig
	mu     sync.RWMutex // protects conn during reconnect
	conn   transport.Connection
	ctx    context.Context
	cancel context.CancelFunc

	reconnecting atomic.Bool
	sem          chan struct{} // nil when MaxPendingRequests == 0 (unlimited)
	onReconnect  func()       // called after successful reconnect; used in tests

	calls          atomic.Int64
	errs           atomic.Int64
	totalLatencyMs atomic.Int64
	estTokensSaved atomic.Int64
	lastCallAt     atomic.Pointer[time.Time]
	lastErrMsg     atomic.Pointer[string]
}

func (u *upstreamServer) shutdown() {
	u.cancel()
}

func (u *upstreamServer) callTool(ctx context.Context, toolName string, args map[string]any) (json.RawMessage, error) {
	if u.sem != nil {
		select {
		case u.sem <- struct{}{}:
			defer func() { <-u.sem }()
		default:
			return nil, fmt.Errorf("upstream %s: request queue full (limit %d)", u.cfg.Name, cap(u.sem))
		}
	}
	params, err := json.Marshal(transport.ToolCallParams{Name: toolName, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	return u.dispatchCall(ctx, params)
}

func (u *upstreamServer) dispatchCall(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	now := time.Now()
	u.calls.Add(1)
	u.lastCallAt.Store(&now)
	raw, err := u.callConn(ctx, params)
	if err != nil {
		return nil, u.classifyCallError(err)
	}
	result, toolErr := extractContent(raw)
	if toolErr != nil {
		u.recordError(toolErr.Error())
	}
	return result, toolErr
}

func (u *upstreamServer) callConn(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	u.mu.RLock()
	raw, err := u.conn.Call(ctx, "tools/call", params)
	u.mu.RUnlock()
	return raw, err
}

func (u *upstreamServer) classifyCallError(err error) error {
	u.recordError(err.Error())
	// Context cancellation/timeout: upstream is still alive — don't reconnect.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	// JSON-RPC application errors (e.g. -32602): not transport failures — don't reconnect.
	var rpcErr *transport.RPCError
	if errors.As(err, &rpcErr) {
		return err
	}
	return connError{err}
}

func (u *upstreamServer) recordError(msg string) {
	u.errs.Add(1)
	u.lastErrMsg.Store(&msg)
}

func (u *upstreamServer) stats() map[string]any {
	st := map[string]any{
		"calls":  u.calls.Load(),
		"errors": u.errs.Load(),
	}
	if t := u.lastCallAt.Load(); t != nil {
		st["last_call"] = t.Format(time.RFC3339)
	}
	if msg := u.lastErrMsg.Load(); msg != nil && *msg != "" {
		st["last_error"] = *msg
	}
	if u.reconnecting.Load() {
		st["status"] = "reconnecting"
	} else {
		st["status"] = "connected"
	}
	if u.sem != nil {
		st["pending"] = len(u.sem)
		st["pending_limit"] = cap(u.sem)
	}
	u.appendPerfStats(st)
	return st
}

func (u *upstreamServer) appendPerfStats(st map[string]any) {
	if totalMs := u.totalLatencyMs.Load(); totalMs > 0 {
		st["total_latency_ms"] = totalMs
		if c := u.calls.Load(); c > 0 {
			st["avg_latency_ms"] = totalMs / c
		}
	}
	if saved := u.estTokensSaved.Load(); saved > 0 {
		st["est_tokens_saved"] = saved
	}
}

// MCP tools/call response: {"content":[{"type":"text","text":"..."}],"isError":false}
// Returns combined text as raw JSON if parseable, else as a JSON string.
func extractContent(raw json.RawMessage) (json.RawMessage, error) {
	var result transport.ToolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("upstream returned non-standard response: %w", err)
	}

	if result.IsError {
		return nil, fmt.Errorf("tool returned error: %s", joinTextContent(result.Content))
	}

	text := joinTextContent(result.Content)
	trimmed := strings.TrimSpace(text)

	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed), nil
	}

	b, err := json.Marshal(trimmed)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func joinTextContent(items []transport.ContentItem) string {
	var parts []string
	for _, item := range items {
		if item.Type == "text" && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}
