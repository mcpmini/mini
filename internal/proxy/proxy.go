// Package proxy provides a stdio↔HTTP bridge that forwards JSON-RPC requests
// to a running mini daemon.
package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

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
	fp := newForwardPool(p, limit)
	defer fp.close()
	scanner := transport.NewScanner(p.In)
	for scanner.Scan() {
		if err := fp.session.writer.Err(); err != nil {
			break
		}
		message := classifyForwardedMessage(maybeInjectToolMode(scanner.Bytes(), p.ToolMode))
		if message.mustForwardSync() {
			if err := forwardSync(message, fp); err != nil {
				fp.wg.Wait()
				return err
			}
			continue
		}
		startForward(message, fp)
	}
	fp.wg.Wait()
	if err := scanner.Err(); err != nil {
		return err
	}
	return fp.session.writer.Err()
}

func newForwardPool(p RunParams, limit int) forwardAsyncParams {
	var wg sync.WaitGroup
	return forwardAsyncParams{
		session: newProxySession(p, newLineWriter(p.Out)),
		wg:      &wg,
		sem:     make(chan struct{}, max(1, limit)),
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

func startForward(message forwardedMessage, p forwardAsyncParams) {
	if len(message.line) == 0 {
		return
	}
	p.message = forwardedMessage{line: bytes.Clone(message.line), kind: message.kind}
	p.sem <- struct{}{}
	p.wg.Add(1)
	go forwardAsync(p)
}

func forwardSync(message forwardedMessage, p forwardAsyncParams) error {
	if len(message.line) == 0 {
		return nil
	}
	cloned := forwardedMessage{line: bytes.Clone(message.line), kind: message.kind}
	if resp := p.session.forward(cloned); resp != nil {
		return p.session.writeLine(resp)
	}
	return nil
}

type forwardAsyncParams struct {
	session *proxySession
	message forwardedMessage
	wg      *sync.WaitGroup
	sem     chan struct{}
}

func (p forwardAsyncParams) close() {
	p.session.close()
}

func forwardAsync(p forwardAsyncParams) {
	defer p.wg.Done()
	defer func() { <-p.sem }()
	if resp := p.session.forward(p.message); resp != nil {
		if err := p.session.writeLine(resp); err != nil {
			return
		}
	}
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

func peekMethod(line []byte) string {
	var m struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(line, &m) != nil {
		return ""
	}
	return m.Method
}

type forwardedMessageKind uint8

const (
	forwardedMessageOther forwardedMessageKind = iota
	forwardedMessageInitialize
	forwardedMessageInitialized
)

type forwardedMessage struct {
	line []byte
	kind forwardedMessageKind
}

func classifyForwardedMessage(line []byte) forwardedMessage {
	switch peekMethod(line) {
	case "initialize":
		return forwardedMessage{line: line, kind: forwardedMessageInitialize}
	case transport.NotificationInitialized:
		return forwardedMessage{line: line, kind: forwardedMessageInitialized}
	default:
		return forwardedMessage{line: line, kind: forwardedMessageOther}
	}
}

func (m forwardedMessage) mustForwardSync() bool {
	return m.kind != forwardedMessageOther
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
