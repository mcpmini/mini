// Package proxy provides a stdio↔HTTP bridge that forwards JSON-RPC requests
// to a running mini daemon.
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/mcpmini/mini/internal/transport"
)

const maxConcurrentForwards = 32

// Run reads JSON-RPC from in, forwards each request to the daemon at port,
// and writes responses back to out. Blocks until EOF.
func Run(port int, sessionID string, in io.Reader, out io.Writer) error {
	return runWithLimit(port, sessionID, in, out, maxConcurrentForwards)
}

func runWithLimit(port int, sessionID string, in io.Reader, out io.Writer, limit int) error {
	// No client-level timeout: tool deadlines are enforced by the daemon's
	// per-call context (ToolTimeout). A fixed timeout here would break any
	// tool configured with tool_timeout longer than the hard-coded value.
	client := &http.Client{}
	scanner := transport.NewScanner(in)
	var outMu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(1, limit))
	for scanner.Scan() {
		startForward(client, scanner.Bytes(), forwardAsyncParams{
			port: port, sessionID: sessionID, out: out, mu: &outMu, wg: &wg, sem: sem,
		})
	}
	wg.Wait()
	return scanner.Err()
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
}

func forwardAsync(p forwardAsyncParams) {
	defer p.wg.Done()
	defer func() { <-p.sem }()
	resp := forward(p.client, p.port, p.sessionID, p.line)
	if resp == nil {
		return
	}
	p.mu.Lock()
	fmt.Fprintf(p.out, "%s\n", resp) //nolint:errcheck
	p.mu.Unlock()
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
