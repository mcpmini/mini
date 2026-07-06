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
	"sync/atomic"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

const maxConcurrentForwards = 32

type RunParams struct {
	Client    *http.Client
	SessionID string
	Token     string
	In        io.Reader
	Out       io.Writer
	ToolMode  transport.ToolMode
	Resolver  *DaemonResolver // nil = standalone, no recovery
	Clock     clock.Clock
}

// DaemonSession is an initialized, authenticated conversation with the daemon.
type DaemonSession struct {
	client    *http.Client
	sessionID string
	token     string
}

func (s DaemonSession) Send(body []byte) []byte {
	return classifyForward(s, body).resp
}

type Forwarder struct {
	session  DaemonSession
	resolver *DaemonResolver
	link     *daemonLink
	toolMode transport.ToolMode
	clock    clock.Clock
	bridge   *notificationBridge
	initSeen atomic.Bool
	ready    atomic.Bool
}

func (f *Forwarder) sessionAt(state linkState) DaemonSession {
	s := f.session
	s.token = state.token
	return s
}

func Run(p RunParams) error {
	return runWithLimit(p, maxConcurrentForwards)
}

func runWithLimit(p RunParams, limit int) error {
	// p.Client has no client-level timeout: tool deadlines are enforced by the daemon's
	// per-call context (ToolTimeout). A fixed timeout here would break any tool configured
	// with tool_timeout longer than the hard-coded value.
	fp := newForwardPool(p, p.Client, limit)
	defer fp.close()
	scanner := transport.NewScanner(p.In)
	for scanner.Scan() {
		line := maybeInjectToolMode(scanner.Bytes(), p.ToolMode)
		if mustForwardSync(line) {
			forwardSync(line, fp)
			continue
		}
		startForward(line, fp)
	}
	fp.wg.Wait()
	return scanner.Err()
}

func newForwardPool(p RunParams, client *http.Client, limit int) forwardAsyncParams {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var bridge *notificationBridge
	if p.ToolMode == transport.ToolModeProxy {
		bridge = newNotificationBridge(
			DaemonSession{client: client, sessionID: p.SessionID},
			p.Out,
			&mu,
			p.Clock,
		)
	}
	forwarder := &Forwarder{
		session:  DaemonSession{client: client, sessionID: p.SessionID},
		resolver: p.Resolver,
		link:     newDaemonLink(p.Token),
		toolMode: p.ToolMode,
		clock:    p.Clock,
		bridge:   bridge,
	}
	return forwardAsyncParams{
		forwarder: forwarder, out: p.Out,
		mu: &mu, wg: &wg, sem: make(chan struct{}, max(1, limit)),
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

func forwardSync(line []byte, p forwardAsyncParams) {
	if len(line) == 0 {
		return
	}
	if resp := p.forwarder.Forward(bytes.Clone(line)); resp != nil {
		p.writeResponse(resp)
	}
}

type forwardAsyncParams struct {
	forwarder *Forwarder
	line      []byte
	out       io.Writer
	mu        *sync.Mutex
	wg        *sync.WaitGroup
	sem       chan struct{}
}

func (p forwardAsyncParams) close() {
	if p.forwarder.bridge != nil {
		p.forwarder.bridge.Close()
	}
}

func forwardAsync(p forwardAsyncParams) {
	defer p.wg.Done()
	defer func() { <-p.sem }()
	if resp := p.forwarder.Forward(p.line); resp != nil {
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

func peekMethod(line []byte) string {
	var msg struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return ""
	}
	return msg.Method
}

func isInitializedNotification(line []byte) bool {
	return peekMethod(line) == transport.NotificationInitialized
}

func mustForwardSync(line []byte) bool {
	method := peekMethod(line)
	return method == "initialize" || method == transport.NotificationInitialized
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
