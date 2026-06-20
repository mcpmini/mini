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

type RunParams struct {
	// Client's transport targets the daemon's socket; one client serves all forwards.
	Client    *http.Client
	SessionID string
	Token     string
	In        io.Reader
	Out       io.Writer
	ToolMode  transport.ToolMode
	// Nil disables self-healing. Otherwise called to respawn the daemon and re-read the token after a failure.
	Reresolve func() (token string, err error)
}

type daemonConn struct {
	client    *http.Client
	sessionID string
	token     string
}

func Run(p RunParams) error {
	return runWithLimit(p, maxConcurrentForwards)
}

func runWithLimit(p RunParams, limit int) error {
	// p.Client has no client-level timeout: tool deadlines are enforced by the daemon's
	// per-call context (ToolTimeout). A fixed timeout here would break any tool configured
	// with tool_timeout longer than the hard-coded value.
	fp := newForwardPool(p, p.Client, limit)
	scanner := transport.NewScanner(p.In)
	for scanner.Scan() {
		startForward(maybeInjectToolMode(scanner.Bytes(), p.ToolMode), fp)
	}
	fp.wg.Wait()
	return scanner.Err()
}

func newForwardPool(p RunParams, client *http.Client, limit int) forwardAsyncParams {
	var mu sync.Mutex
	var wg sync.WaitGroup
	link := newDaemonLink(p.Token, p.Reresolve)
	return forwardAsyncParams{
		client: client, link: link, sessionID: p.SessionID, out: p.Out,
		mu: &mu, wg: &wg, sem: make(chan struct{}, max(1, limit)),
		toolMode: p.ToolMode,
	}
}

func maybeInjectToolMode(line []byte, mode transport.ToolMode) []byte {
	if mode == transport.ToolModeCompact {
		return injectCompactMode(line)
	}
	return line
}

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

func startForward(line []byte, p forwardAsyncParams) {
	if len(line) == 0 {
		return
	}
	p.line = bytes.Clone(line)
	p.sem <- struct{}{}
	p.wg.Add(1)
	go forwardAsync(p)
}

type forwardAsyncParams struct {
	client    *http.Client
	link      *daemonLink
	sessionID string
	line      []byte
	out       io.Writer
	mu        *sync.Mutex
	wg        *sync.WaitGroup
	sem       chan struct{}
	toolMode  transport.ToolMode
}

func (p forwardAsyncParams) connAt(s linkState) daemonConn {
	return daemonConn{client: p.client, sessionID: p.sessionID, token: s.token}
}

func forwardAsync(p forwardAsyncParams) {
	defer p.wg.Done()
	defer func() { <-p.sem }()
	if resp := p.deliver(); resp != nil {
		p.writeResponse(resp)
	}
}

func (p forwardAsyncParams) writeResponse(resp []byte) {
	p.mu.Lock()
	fmt.Fprintf(p.out, "%s\n", resp) //nolint:errcheck
	p.mu.Unlock()
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
		strings.HasPrefix(rpc.Error.Message, transport.NotInitializedMessage)
}

func peekIsInitialize(line []byte) bool {
	var m struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(line, &m) == nil && m.Method == "initialize"
}

func forward(conn daemonConn, body []byte) []byte {
	return classifyForward(conn, body).resp
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
