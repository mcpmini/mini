package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
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
	cfg      config.ServerConfig
	mu       sync.RWMutex // protects conn during reconnect
	conn     transport.Connection
	lastDefs []transport.ToolDefinition
	ctx      context.Context
	cancel   context.CancelFunc
	clock    clock.Clock

	reconnecting atomic.Bool
	sem          chan struct{} // nil when MaxPendingRequests == 0 (unlimited)
	onReconnect  func()        // called after successful reconnect; used in tests

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

func (u *upstreamServer) shutdownAndClose() {
	u.shutdown()
	u.mu.Lock()
	u.conn.Close()
	u.mu.Unlock()
}

func (u *upstreamServer) callTool(ctx context.Context, toolName string, args map[string]any) (json.RawMessage, error) {
	if err := u.acquireSem(); err != nil {
		return nil, err
	}
	if u.sem != nil {
		defer func() { <-u.sem }()
	}
	params, err := json.Marshal(transport.ToolCallParams{Name: toolName, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	return u.dispatchCall(ctx, params)
}

func (u *upstreamServer) acquireSem() error {
	if u.sem == nil {
		return nil
	}
	select {
	case u.sem <- struct{}{}:
		return nil
	default:
		slog.Warn("upstream request queue full", "server", u.cfg.Name, "limit", cap(u.sem))
		return fmt.Errorf("upstream %s: request queue full (limit %d)", u.cfg.Name, cap(u.sem))
	}
}

func (u *upstreamServer) dispatchCall(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	now := u.clock.Now()
	u.calls.Add(1)
	u.lastCallAt.Store(&now)
	raw, err := u.callConn(ctx, params)
	if err != nil {
		return nil, u.classifyCallError(err)
	}
	result, toolErr := invoke.ExtractContent(raw)
	if toolErr != nil {
		u.recordError(toolErr.Error())
	}
	return result, toolErr
}

func (u *upstreamServer) callConn(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	// Read conn under the lock, then release before the network call so a
	// concurrent reconnect can take the write lock without waiting for all
	// in-flight calls to finish. HTTPConnection.Close is a no-op so a
	// swap-during-call is safe; StdioConnection kills the subprocess, which
	// unblocks the call with an error.
	u.mu.RLock()
	conn := u.conn
	u.mu.RUnlock()
	return conn.Call(ctx, "tools/call", params)
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

const maxErrMsgBytes = 512

func (u *upstreamServer) recordError(msg string) {
	u.errs.Add(1)
	if len(msg) > maxErrMsgBytes {
		msg = msg[:maxErrMsgBytes] + "…"
	}
	u.lastErrMsg.Store(&msg)
}

func (u *upstreamServer) stats() map[string]any {
	st := map[string]any{
		"calls":  u.calls.Load(),
		"errors": u.errs.Load(),
	}
	u.appendLastCall(st)
	u.appendLastError(st)
	u.appendStatus(st)
	u.appendPending(st)
	u.appendPerfStats(st)
	return st
}

func (u *upstreamServer) appendLastCall(st map[string]any) {
	if t := u.lastCallAt.Load(); t != nil {
		st["last_call"] = t.Format(time.RFC3339)
	}
}

func (u *upstreamServer) appendLastError(st map[string]any) {
	if msg := u.lastErrMsg.Load(); msg != nil && *msg != "" {
		st["last_error"] = *msg
	}
}

func (u *upstreamServer) appendStatus(st map[string]any) {
	if u.reconnecting.Load() {
		st["status"] = "reconnecting"
		return
	}
	st["status"] = "connected"
}

func (u *upstreamServer) appendPending(st map[string]any) {
	if u.sem == nil {
		return
	}
	st["pending"] = len(u.sem)
	st["pending_limit"] = cap(u.sem)
}

func (u *upstreamServer) appendPerfStats(st map[string]any) {
	if c := u.calls.Load(); c > 0 {
		totalMs := u.totalLatencyMs.Load()
		st["total_latency_ms"] = totalMs
		st["avg_latency_ms"] = totalMs / c
	}
	if saved := u.estTokensSaved.Load(); saved > 0 {
		st["est_tokens_saved"] = saved
	}
}

func (u *upstreamServer) recordSaved(session *Session, latencyMs, saved int64) {
	if saved > 0 {
		u.estTokensSaved.Add(saved)
	}
	session.recordCall(latencyMs, saved, false)
}
