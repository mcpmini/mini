package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const Version = "0.1.0"

// HTTPConnection implements Connection for streamable HTTP / SSE MCP servers.
// The GitHub MCP and similar servers use this transport: each call is a POST,
// responses may be SSE-wrapped, and a session ID is tracked across calls.
type HTTPConnection struct {
	url              string
	headers          map[string]string
	disableRetryOnRateLimit bool
	client           *http.Client
	nextID           atomic.Int64
	sessionID        string
	mu               sync.Mutex
}

const defaultHTTPClientTimeout = 10 * time.Minute

type HTTPConnectionConfig struct {
	URL             string
	Headers         map[string]string
	// ClientTimeout is the hard network-level deadline. Zero means 10 minutes.
	ClientTimeout time.Duration
	// DisableRetryOnRateLimit disables automatic 429/503 retry. When true,
	// rate-limit errors are returned immediately so the caller can decide.
	DisableRetryOnRateLimit bool
}

func NewHTTPConnection(cfg HTTPConnectionConfig) (*HTTPConnection, error) {
	timeout := defaultHTTPClientTimeout
	if cfg.ClientTimeout > 0 {
		timeout = cfg.ClientTimeout
	}
	return &HTTPConnection{
		url:             cfg.URL,
		headers:         cfg.Headers,
		disableRetryOnRateLimit: cfg.DisableRetryOnRateLimit,
		client: &http.Client{
			Timeout: timeout,
			// Don't follow redirects — they can be used to exfiltrate session tokens
			// to a different host than the one the user configured.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

const maxRetries = 3

func (c *HTTPConnection) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	return c.post(ctx, req)
}

type postResult struct {
	body      json.RawMessage
	retryable bool
	delay     time.Duration // -1 = no Retry-After header; caller uses backoff
}

func (c *HTTPConnection) post(ctx context.Context, rpcReq Request) (json.RawMessage, error) {
	backoff := time.Second
	for i := range maxRetries {
		r, err := c.doPost(ctx, rpcReq)
		if err == nil {
			return r.body, nil
		}
		if !r.retryable || i == maxRetries-1 || c.disableRetryOnRateLimit {
			return nil, err
		}
		delay := r.delay
		if delay < 0 {
			delay = backoff
			backoff *= 2
		}
		if !sleepCtx(ctx, delay) {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("exceeded max retries for %s", rpcReq.Method)
}

func (c *HTTPConnection) doPost(ctx context.Context, rpcReq Request) (postResult, error) {
	reqBody, err := json.Marshal(rpcReq)
	if err != nil {
		return postResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(reqBody))
	if err != nil {
		return postResult{}, err
	}
	c.setRequestHeaders(httpReq)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return postResult{}, fmt.Errorf("http %s: %w", rpcReq.Method, err)
	}
	defer resp.Body.Close()
	return c.processResponse(resp, rpcReq.Method)
}

func (c *HTTPConnection) processResponse(resp *http.Response, method string) (postResult, error) {
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && len(sid) <= 256 {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		delay := parseRetryAfter(resp.Header.Get("Retry-After"))
		return postResult{retryable: true, delay: delay}, fmt.Errorf("http %s: status %d: %s", method, resp.StatusCode, errBody)
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return postResult{}, fmt.Errorf("http %s: status %d: %s", method, resp.StatusCode, errBody)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return postResult{}, fmt.Errorf("read response: %w", err)
	}
	result, err := parseHTTPBody(respBody)
	return postResult{body: result}, err
}

// parseRetryAfter returns the delay from a Retry-After header value (seconds).
// Returns -1 if header is absent or unparseable (signal: retry with backoff).
func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return -1
	}
	const maxDelay = 60 * time.Second
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxDelay)
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, maxDelay)
		}
	}
	return -1
}

func (c *HTTPConnection) setRequestHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Spec (Streamable HTTP): client MUST include MCP-Protocol-Version on all
	// post-initialize requests so the server can reject version mismatches.
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	req.Header.Set("X-Minimcp-Version", Version)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	select {
	case <-ctx.Done():
		t.Stop()
		return false
	case <-t.C:
		return true
	}
}

// parseHTTPBody handles both plain JSON and SSE-wrapped JSON responses.
// SSE format: "event: message\ndata: {...json...}\n\n"
func parseHTTPBody(body []byte) (json.RawMessage, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, nil
	}

	jsonBytes := body
	if bytes.HasPrefix(body, []byte("event:")) || bytes.HasPrefix(body, []byte("data:")) {
		jsonBytes = extractSSEData(body)
	}

	var rpc Response
	if err := json.Unmarshal(jsonBytes, &rpc); err != nil {
		return nil, fmt.Errorf("parse response: %w: %s", err, jsonBytes[:min(200, len(jsonBytes))])
	}
	if rpc.Error != nil {
		return nil, rpc.Error
	}
	return rpc.Result, nil
}

func extractSSEData(body []byte) []byte {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			return []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return body
}

func (c *HTTPConnection) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	if err := c.initHandshake(ctx); err != nil {
		return nil, err
	}
	raw, err := c.Call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var r ToolsListResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return toToolDefs(r.Tools), nil
}

func (c *HTTPConnection) initHandshake(ctx context.Context) error {
	if err := c.sendInitialize(ctx); err != nil {
		return err
	}
	return c.sendInitializedNotification(ctx)
}

func (c *HTTPConnection) sendInitialize(ctx context.Context) error {
	params, _ := json.Marshal(InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mini", Version: Version},
	})
	_, err := c.Call(ctx, "initialize", params)
	return err
}

// Spec: client MUST send notifications/initialized after a successful
// initialize response. Stdio sends this in stdio.go:initialize; HTTP must
// send it here as a separate POST with no expected response (notification).
func (c *HTTPConnection) sendInitializedNotification(ctx context.Context) error {
	notif, _ := json.Marshal(Notification{JSONRPC: "2.0", Method: NotificationInitialized})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(notif))
	if err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	c.setRequestHeaders(httpReq)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (c *HTTPConnection) Health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("server error: %d", resp.StatusCode)
	}
	return nil
}

func (c *HTTPConnection) Close() error { return nil }
