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
)

const maxConcurrentForwards = 32

// RunParams configures a daemon proxy run.
type RunParams struct {
	Port      int
	SessionID string
	In        io.Reader
	Out       io.Writer
	Compact   bool
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
		mu: &mu, wg: &wg, sem: make(chan struct{}, max(1, limit)),
		compact: p.Compact,
	}
}

// maybeInjectToolMode signals compact mode to the daemon. Passthrough is the
// daemon's zero-value default, so it injects nothing.
func maybeInjectToolMode(line []byte, compact bool) []byte {
	if compact {
		return injectCompactMode(line)
	}
	return line
}

// injectCompactMode inserts "_mini_tool_mode": "compact" into the params of an
// initialize JSON-RPC message so the daemon uses the compact interface for this
// session. Non-initialize messages and parse errors are returned unchanged.
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
	line      []byte
	out       io.Writer
	mu        *sync.Mutex
	wg        *sync.WaitGroup
	sem       chan struct{}
	compact   bool
}

func forwardAsync(p forwardAsyncParams) {
	defer p.wg.Done()
	defer func() { <-p.sem }()
	resp := forward(p.client, p.port, p.sessionID, p.line)
	if resp == nil {
		return
	}
	// Daemon restart or session eviction invalidates the session; reinitialize and retry.
	if isNotInitialized(resp) && !peekIsInitialize(p.line) {
		reinitDaemon(p.client, p.port, p.sessionID, p.compact)
		resp = forward(p.client, p.port, p.sessionID, p.line)
		if resp == nil {
			return
		}
	}
	p.mu.Lock()
	fmt.Fprintf(p.out, "%s\n", resp) //nolint:errcheck
	p.mu.Unlock()
}

// reinitDaemon recovers from daemon restart or session eviction. Responses are
// discarded — only the caller's retry of the original request is forwarded.
func reinitDaemon(client *http.Client, port int, sessionID string, compact bool) {
	params, _ := json.Marshal(transport.InitializeParams{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      transport.ClientInfo{Name: "mini", Version: transport.Version},
	})
	initMsg, _ := json.Marshal(transport.Request{JSONRPC: "2.0", ID: -1, Method: "initialize", Params: json.RawMessage(params)})
	forward(client, port, sessionID, maybeInjectToolMode(initMsg, compact))
	notif, _ := json.Marshal(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationInitialized})
	forward(client, port, sessionID, notif)
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
	var m struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(line, &m) == nil && m.Method == "initialize"
}

func forward(client *http.Client, port int, sessionID string, body []byte) []byte {
	req, err := newDaemonRequest(port, sessionID, body)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return daemonErrorResponse(body, "daemon unreachable: "+err.Error())
	}
	defer resp.Body.Close()
	return readForwardResponse(resp)
}

func newDaemonRequest(port int, sessionID string, body []byte) (*http.Request, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body)) //nolint:noctx // no context at proxy level; daemon enforces per-call timeouts
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	return req, nil
}

func readForwardResponse(resp *http.Response) []byte {
	if resp.StatusCode == http.StatusAccepted {
		return nil // notification — no response expected
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
