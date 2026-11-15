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

// upstreamServer is one incarnation of a server: from the start of its first connect until it is
// stopped. A config edit or a login replaces it with a new incarnation.
type upstreamServer struct {
	cfg        config.ServerConfig
	fromConfig bool                // started from servers/, the only servers a config edit removes
	saved      config.ServerConfig // own copy of the settings, compared with the file on each check
	removeGen  uint64
	openedAt   time.Time // start of the startup hold
	ctx        context.Context
	cancel     context.CancelFunc
	clock      clock.Clock

	mu       sync.RWMutex
	conn     transport.Connection // nil until the first connect succeeds
	lastDefs []transport.ToolDefinition
	ready    bool // set once the registry has the tools
	failure  startupFailure
	// loginSavedAt is the token file's time when the server stopped at needs auth. Taken then, not
	// before connecting, so a token mini refreshed during that attempt doesn't count as a new login.
	loginSavedAt time.Time
	// OAuth mini detected on a failure, which every later load merges in. Compared with it, the next
	// check doesn't take the note for an edit of the file.
	detectedAuth *config.AuthConfig

	reconnecting atomic.Bool
	sem          chan struct{} // nil when MaxPendingRequests == 0 (unlimited)
	onReconnect  func()        // called after successful reconnect; used in tests
	refresher    toolListRefresher

	calls          atomic.Int64
	errs           atomic.Int64
	totalLatencyMs atomic.Int64
	estTokensSaved atomic.Int64
	lastCallAt     atomic.Pointer[time.Time]
	lastErrMsg     atomic.Pointer[string]
}

func (u *upstreamServer) adoptToolList(toolList transport.ToolList) {
	u.lastDefs = toolList.Tools
	u.refresher.published(toolList.Freshness)
}

func (u *upstreamServer) shutdown() {
	u.refresher.stop()
	u.cancel()
}

func (u *upstreamServer) shutdownAndClose() {
	u.shutdown()
	// Outside u.mu: HTTP Close waits for notification callbacks that take u.mu. Once u.ctx is done no
	// reconnect can swap u.conn, so this is the last connection.
	if conn := u.currentConn(); conn != nil {
		conn.Close() //nolint:errcheck // Server removal has no caller to receive connection cleanup errors.
	}
}

type callToolParams struct {
	Tool string
	Args map[string]any
	MRTR *transport.ToolCallMRTR
}

func (u *upstreamServer) callTool(ctx context.Context, p callToolParams) (json.RawMessage, error) {
	if err := u.acquireSem(); err != nil {
		return nil, err
	}
	if u.sem != nil {
		defer func() { <-u.sem }()
	}
	params, err := json.Marshal(transport.NewToolCallParams(p.Tool, p.Args, p.MRTR))
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
	conn := u.currentConn()
	if conn == nil {
		// Looked up just before a replace; the new incarnation hasn't connected yet.
		return nil, fmt.Errorf("server %q is still connecting; try again in a few seconds", u.cfg.Name)
	}
	raw, err := callConn(ctx, conn, params)
	if err != nil {
		return nil, u.recordCallError(err)
	}
	toolErr := invoke.CheckResult(raw)
	if toolErr != nil && !errors.Is(toolErr, invoke.ErrInputRequired) {
		u.recordError(toolErr.Error())
	}
	return raw, toolErr
}

func callConn(ctx context.Context, conn transport.Connection, params json.RawMessage) (json.RawMessage, error) {
	// Not under u.mu, so a reconnect needn't wait for in-flight calls. A swap
	// during a call is safe: HTTPConnection.Close stops only the notification
	// listener, and StdioConnection.Close kills the subprocess, which fails the
	// call with an error.
	return conn.Call(ctx, "tools/call", params)
}

func (u *upstreamServer) currentConn() transport.Connection {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.conn
}

func (u *upstreamServer) recordCallError(err error) error {
	u.recordError(err.Error())
	return classifyCallError(err)
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
