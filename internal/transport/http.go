package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/version"
)


// HTTPConnection implements Connection for streamable HTTP / SSE MCP servers.
// The GitHub MCP and similar servers use this transport: each call is a POST,
// responses may be SSE-wrapped, and a session ID is tracked across calls.
type HTTPConnection struct {
	url                     string
	headers                 map[string]string
	disableRetryOnRateLimit bool
	client                  *http.Client
	clock                   clock.Clock
	nextID                  atomic.Int64
	sessionID               string
	mu                      sync.Mutex
}

// defaultHTTPClientTimeout is the hard network-level backstop. Set to 2× the default
// ToolTimeout (30s) so the per-call deadline fires first in normal operation and this
// only activates when context cancellation fails at the OS/network level. Users with
// slow tools (tool_timeout > 60s) should set http_client_timeout in their server YAML.
const defaultHTTPClientTimeout = 60 * time.Second

type HTTPConnectionConfig struct {
	URL     string
	Headers map[string]string
	// ClientTimeout is the hard network-level deadline. Zero means 60 seconds.
	ClientTimeout time.Duration
	// DisableRetryOnRateLimit disables automatic 429/503 retry. When true,
	// rate-limit errors are returned immediately so the caller can decide.
	DisableRetryOnRateLimit bool
	// BlockPrivateIPs attaches an SSRF-safe dialer that re-validates resolved IPs
	// at connect time, preventing DNS rebinding attacks. Set for runtime-added servers.
	BlockPrivateIPs bool
}

func NewHTTPConnection(cfg HTTPConnectionConfig) (*HTTPConnection, error) {
	timeout := defaultHTTPClientTimeout
	if cfg.ClientTimeout > 0 {
		timeout = cfg.ClientTimeout
	}
	return &HTTPConnection{
		url:                     cfg.URL,
		headers:                 cfg.Headers,
		disableRetryOnRateLimit: cfg.DisableRetryOnRateLimit,
		client:                  noRedirectClient(timeout, cfg.BlockPrivateIPs),
		clock:                   clock.System(),
	}, nil
}

// noRedirectClient blocks redirects to prevent session token exfiltration to a different host.
func noRedirectClient(timeout time.Duration, blockPrivateIPs bool) *http.Client {
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if blockPrivateIPs {
		applySsrfTransport(client)
	}
	return client
}

func applySsrfTransport(client *http.Client) {
	dt, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		t := dt.Clone()
		t.DialContext = SSRFSafeDialer()
		client.Transport = t
	}
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
		body, done, err := c.postWithRetryDelay(ctx, rpcReq, i, &backoff)
		if done {
			return body, err
		}
	}
	return nil, fmt.Errorf("exceeded max retries for %s", rpcReq.Method)
}

func (c *HTTPConnection) postWithRetryDelay(ctx context.Context, rpcReq Request, i int, backoff *time.Duration) (json.RawMessage, bool, error) {
	r, err := c.doPost(ctx, rpcReq)
	if err == nil {
		return r.body, true, nil
	}
	if shouldStopRetrying(r, i, c.disableRetryOnRateLimit) {
		return nil, true, err
	}
	delay := nextRetryDelay(r.delay, backoff)
	slog.Warn("upstream http request retrying", "method", rpcReq.Method, "attempt", i+1, "delay", delay)
	if !c.sleepCtx(ctx, delay) {
		return nil, true, ctx.Err()
	}
	return nil, false, nil
}

func shouldStopRetrying(r postResult, attempt int, retriesDisabled bool) bool {
	return !r.retryable || attempt == maxRetries-1 || retriesDisabled
}

func nextRetryDelay(delay time.Duration, backoff *time.Duration) time.Duration {
	if delay >= 0 {
		return delay
	}
	delay = *backoff
	*backoff *= 2
	return delay
}

func (c *HTTPConnection) doPost(ctx context.Context, rpcReq Request) (postResult, error) {
	httpReq, err := c.buildHTTPRequest(ctx, rpcReq)
	if err != nil {
		return postResult{}, err
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return postResult{}, fmt.Errorf("http %s: %w", rpcReq.Method, err)
	}
	defer resp.Body.Close()
	return c.processResponse(resp, rpcReq.Method)
}

func (c *HTTPConnection) buildHTTPRequest(ctx context.Context, rpcReq Request) (*http.Request, error) {
	reqBody, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(httpReq)
	return httpReq, nil
}

func (c *HTTPConnection) processResponse(resp *http.Response, method string) (postResult, error) {
	c.storeSessionID(resp.Header.Get("Mcp-Session-Id"))
	if resp.StatusCode >= 400 {
		return c.httpErrorResult(resp, method)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return postResult{}, fmt.Errorf("read response: %w", err)
	}
	result, err := parseHTTPBody(respBody)
	return postResult{body: result}, err
}

func (c *HTTPConnection) storeSessionID(sessionID string) {
	if sessionID == "" || len(sessionID) > 256 {
		return
	}
	c.mu.Lock()
	c.sessionID = sessionID
	c.mu.Unlock()
}

func (c *HTTPConnection) httpErrorResult(resp *http.Response, method string) (postResult, error) {
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if isRetryableStatus(resp.StatusCode) {
		return postResult{
			retryable: true,
			delay:     parseRetryAfter(resp.Header.Get("Retry-After"), c.clock.Now()),
		}, fmt.Errorf("http %s: status %d: %s", method, resp.StatusCode, errBody)
	}
	return postResult{}, fmt.Errorf("http %s: status %d: %s", method, resp.StatusCode, errBody)
}

func isRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable
}

// parseRetryAfter returns the delay from a Retry-After header value (seconds).
// Returns -1 if header is absent or unparseable (signal: retry with backoff).
func parseRetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return -1
	}
	const maxDelay = 60 * time.Second
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxDelay)
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := t.Sub(now); d > 0 {
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
	req.Header.Set("X-Mini-Version", version.Version)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()
}

func (c *HTTPConnection) sleepCtx(ctx context.Context, d time.Duration) bool {
	t := c.clock.NewTimer(d)
	select {
	case <-ctx.Done():
		t.Stop()
		return false
	case <-t.C():
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
	jsonBytes := unwrapHTTPBody(body)
	var rpc Response
	if err := json.Unmarshal(jsonBytes, &rpc); err != nil {
		return nil, fmt.Errorf("parse response: %w: %s", err, jsonBytes[:min(200, len(jsonBytes))])
	}
	if rpc.Error != nil {
		return nil, rpc.Error
	}
	return rpc.Result, nil
}

func unwrapHTTPBody(body []byte) []byte {
	if bytes.HasPrefix(body, []byte("event:")) || bytes.HasPrefix(body, []byte("data:")) {
		return extractSSEData(body)
	}
	return body
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
	return paginateToolsList(ctx, c.callToolsPage)
}

func (c *HTTPConnection) callToolsPage(ctx context.Context, cursor string) (ToolsListResult, error) {
	var params json.RawMessage
	if cursor != "" {
		params, _ = json.Marshal(map[string]string{"cursor": cursor})
	}
	raw, err := c.Call(ctx, "tools/list", params)
	if err != nil {
		return ToolsListResult{}, err
	}
	var r ToolsListResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return ToolsListResult{}, fmt.Errorf("parse tools/list: %w", err)
	}
	return r, nil
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
		ClientInfo:      ClientInfo{Name: "mini", Version: version.Version},
	})
	_, err := c.Call(ctx, "initialize", params)
	return err
}

// Client MUST send notifications/initialized after a successful initialize response.
// Stdio sends this in stdio.go:initialize; HTTP must send it as a separate POST.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L88
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
