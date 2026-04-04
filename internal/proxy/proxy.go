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
	if limit < 1 {
		limit = 1
	}
	// No client-level timeout: tool deadlines are enforced by the daemon's
	// per-call context (ToolTimeout). A fixed timeout here would break any
	// tool configured with tool_timeout longer than the hard-coded value.
	client := &http.Client{}
	scanner := transport.NewScanner(in)
	var outMu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)
	for scanner.Scan() {
		line := copyLine(scanner.Bytes())
		if line == nil {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go forwardAsync(forwardAsyncParams{client: client, port: port, sessionID: sessionID, line: line, out: out, mu: &outMu, wg: &wg, sem: sem})
	}
	wg.Wait()
	return scanner.Err()
}

func copyLine(line []byte) []byte {
	if len(line) == 0 {
		return nil
	}
	cp := make([]byte, len(line))
	copy(cp, line)
	return cp
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
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body)) //nolint:noctx // no context at proxy level; daemon enforces per-call timeouts
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)

	resp, err := client.Do(req)
	if err != nil {
		return daemonErrorResponse(body, "daemon unreachable: "+err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		return nil // notification — no response expected
	}
	result, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return bytes.TrimSpace(result)
}

func daemonErrorResponse(body []byte, msg string) []byte {
	var req struct {
		ID   json.RawMessage `json:"id"`
		JSONRPC string `json:"jsonrpc"`
	}
	json.Unmarshal(body, &req) //nolint:errcheck
	if req.ID == nil || req.JSONRPC == "" {
		return nil // notification — no id to reply to
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"error":   map[string]any{"code": -32603, "message": msg},
	}
	b, _ := json.Marshal(resp)
	return b
}
