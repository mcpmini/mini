// Package proxy provides a stdio↔HTTP bridge that forwards JSON-RPC requests
// to a running mini daemon.
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

const maxConcurrentForwards = 32

// RunParams configures a daemon proxy run.
type RunParams struct {
	Port      int
	SessionID string
	Token     string
	// ReloadToken re-reads the token after a daemon restart rotates it; the proxy
	// calls it on a 401, then retries. Nil disables refresh.
	ReloadToken func() (string, error)
	In          io.Reader
	Out         io.Writer
	Compact     bool
}

// daemonConn identifies the target daemon and the credentials for one forward.
type daemonConn struct {
	client    *http.Client
	port      int
	sessionID string
	token     string
}

// Run reads JSON-RPC from p.In, forwards each request to the daemon at p.Port,
// and writes responses back to p.Out. Blocks until EOF.
func Run(p RunParams) error {
	return runWithLimit(p, maxConcurrentForwards)
}

func runWithLimit(p RunParams, limit int) error {
	// No client-level timeout: tool deadlines are enforced by the daemon's
	// per-call context (ToolTimeout). A fixed timeout here would break any
	// tool configured with tool_timeout longer than the hard-coded value.
	client := &http.Client{}
	fp := newForwardPool(p, limit)
	scanner := transport.NewScanner(p.In)
	for scanner.Scan() {
		startForward(client, maybeInjectToolMode(scanner.Bytes(), p.Compact), fp)
	}
	fp.wg.Wait()
	return scanner.Err()
}

func newForwardPool(p RunParams, limit int) forwardAsyncParams {
	var mu sync.Mutex
	var wg sync.WaitGroup
	return forwardAsyncParams{
		port: p.Port, sessionID: p.SessionID, out: p.Out,
		tokens: &tokenSource{value: p.Token, reload: p.ReloadToken},
		mu:     &mu, wg: &wg, sem: make(chan struct{}, max(1, limit)),
		compact: p.Compact,
	}
}

func maybeInjectToolMode(line []byte, compact bool) []byte {
	if compact {
		return injectCompactMode(line)
	}
	return line
}

// injectCompactMode only modifies initialize messages; non-initialize messages
// and parse errors are returned unchanged.
func injectCompactMode(line []byte) []byte {
	if !peekIsInitialize(line) {
		return line
	}
	if result, err := withCompactModeParam(line); err == nil {
		return result
	}
	return line
}

func withCompactModeParam(line []byte) ([]byte, error) {
	var full map[string]json.RawMessage
	if err := json.Unmarshal(line, &full); err != nil {
		return nil, err
	}
	params, err := extractInitParams(full)
	if err != nil {
		return nil, err
	}
	params[transport.ToolModeParam] = json.RawMessage(`"` + transport.ToolModeCompactValue + `"`)
	if full["params"], err = json.Marshal(params); err != nil {
		return nil, err
	}
	return json.Marshal(full)
}

func extractInitParams(full map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	params := map[string]json.RawMessage{}
	if raw := full["params"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
	}
	return params, nil
}

func startForward(client *http.Client, line []byte, p forwardAsyncParams) {
	if len(line) == 0 {
		return
	}
	p.line = bytes.Clone(line)
	p.client = client
	p.sem <- struct{}{}
	p.wg.Add(1)
	go forwardAsync(p)
}

type forwardAsyncParams struct {
	client    *http.Client
	port      int
	sessionID string
	tokens    *tokenSource
	line      []byte
	out       io.Writer
	mu        *sync.Mutex
	wg        *sync.WaitGroup
	sem       chan struct{}
	compact   bool
}

func (p forwardAsyncParams) conn() daemonConn {
	return daemonConn{client: p.client, port: p.port, sessionID: p.sessionID, token: p.tokens.current()}
}

func forwardAsync(p forwardAsyncParams) {
	defer p.wg.Done()
	defer func() { <-p.sem }()
	resp := forwardWithAuthRetry(p)
	if resp == nil {
		return
	}
	// Daemon restart or session eviction invalidates the session; reinitialize and retry.
	if isNotInitialized(resp) && !peekIsInitialize(p.line) {
		reinitDaemon(p.conn(), p.compact)
		if resp, _ = forward(p.conn(), p.line); resp == nil {
			return
		}
	}
	p.mu.Lock()
	fmt.Fprintf(p.out, "%s\n", resp) //nolint:errcheck
	p.mu.Unlock()
}

func forwardWithAuthRetry(p forwardAsyncParams) []byte {
	resp, status := forward(p.conn(), p.line)
	if status != http.StatusUnauthorized {
		return resp
	}
	p.tokens.refresh() // 401: daemon restarted and rotated the token — pick up the new one
	resp, status = forward(p.conn(), p.line)
	if status == http.StatusUnauthorized {
		return daemonErrorResponse(p.line, "daemon rejected credentials") // don't leak the raw 401 body
	}
	return resp
}

// reinitDaemon recovers from daemon restart or session eviction. Responses are
// discarded — only the caller's retry of the original request is forwarded.
func reinitDaemon(conn daemonConn, compact bool) {
	params, _ := json.Marshal(transport.InitializeParams{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      transport.ClientInfo{Name: "mini", Version: version.Version},
	})
	initMsg, _ := json.Marshal(transport.Request{JSONRPC: "2.0", ID: -1, Method: "initialize", Params: json.RawMessage(params)})
	forward(conn, maybeInjectToolMode(initMsg, compact))
	notif, _ := json.Marshal(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationInitialized})
	forward(conn, notif)
}

func isNotInitialized(resp []byte) bool {
	if len(resp) == 0 {
		return false
	}
	var rpc struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	return json.Unmarshal(resp, &rpc) == nil &&
		rpc.Error != nil &&
		strings.HasPrefix(rpc.Error.Message, "not initialized")
}

func peekIsInitialize(line []byte) bool {
	var m struct{ Method string `json:"method"` }
	return json.Unmarshal(line, &m) == nil && m.Method == "initialize"
}

func forward(conn daemonConn, body []byte) ([]byte, int) {
	req, err := newDaemonRequest(conn, body)
	if err != nil {
		return nil, 0
	}
	resp, err := conn.client.Do(req)
	if err != nil {
		return daemonErrorResponse(body, "daemon unreachable: "+err.Error()), 0 // 0: unreachable, not an HTTP status
	}
	defer resp.Body.Close()
	return readForwardResponse(resp, body), resp.StatusCode
}

func newDaemonRequest(conn daemonConn, body []byte) (*http.Request, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", conn.port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body)) //nolint:noctx // no context at proxy level; daemon enforces per-call timeouts
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", conn.sessionID)
	if conn.token != "" {
		req.Header.Set("Authorization", "Bearer "+conn.token)
	}
	return req, nil
}

func readForwardResponse(resp *http.Response, reqBody []byte) []byte {
	if resp.StatusCode == http.StatusAccepted {
		return nil // notification — no response expected
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return daemonErrorResponse(reqBody, fmt.Sprintf("daemon returned HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(errBody)))
	}
	result, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return bytes.TrimSpace(result)
}

type requestID struct {
	ID      json.RawMessage `json:"id"`
	JSONRPC string          `json:"jsonrpc"`
}

func daemonErrorResponse(body []byte, msg string) []byte {
	var req requestID
	json.Unmarshal(body, &req) //nolint:errcheck
	if req.ID == nil || req.JSONRPC == "" {
		return nil // notification — no id to reply to
	}
	return marshalErrorResponse(req.ID, msg)
}

func marshalErrorResponse(id json.RawMessage, msg string) []byte {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": -32603, "message": msg},
	}
	b, _ := json.Marshal(resp)
	return b
}
