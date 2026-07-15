package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// AuthorizationProvider supplies a dynamic Authorization header value for HTTP
// upstreams, refreshing near-expiry OAuth tokens as needed. Declared here rather
// than in internal/auth (which owns the concrete implementation) because
// internal/auth imports internal/transport for endpoint validation; the reverse
// import would cycle.
type AuthorizationProvider interface {
	Authorization(ctx context.Context) (string, error)
	RefreshAuthorization(ctx context.Context) (string, error)
}

// HTTPConnection implements Connection for streamable HTTP / SSE MCP servers.
// The GitHub MCP and similar servers use this transport: each call is a POST,
// responses may be SSE-wrapped, and a session ID is tracked across calls.
type HTTPConnection struct {
	url                     string
	serverName              string
	headers                 map[string]string
	authProvider            AuthorizationProvider
	authHeaderName          string
	disableRetryOnRateLimit bool
	client                  *http.Client
	clock                   clock.Clock
	nextID                  atomic.Int64
	sessionID               string
	mu                      sync.Mutex
	initMu                  sync.Mutex
	initialized             bool
	listenerCtx             context.Context
	listenerCancel          context.CancelFunc
	listenerWG              sync.WaitGroup
	toolsChanged            toolsChangedNotifier
}

// defaultHTTPClientTimeout is the hard network-level backstop. Set to 2× the default
// ToolTimeout (30s) so the per-call deadline fires first in normal operation and this
// only activates when context cancellation fails at the OS/network level. Users with
// slow tools (tool_timeout > 60s) should set http_client_timeout in their server YAML.
const defaultHTTPClientTimeout = 60 * time.Second

// UnauthorizedError carries the WWW-Authenticate header from a 401 response so callers
// can distinguish an OAuth challenge (RFC 9728 §5.1) from a bare auth failure.
type UnauthorizedError struct {
	WWWAuthenticate string
}

func (e *UnauthorizedError) Error() string {
	return fmt.Sprintf("unauthorized (WWW-Authenticate: %q)", e.WWWAuthenticate)
}

type HTTPConnectionConfig struct {
	URL     string
	Headers map[string]string
	Clock   clock.Clock
	// ClientTimeout is the hard network-level deadline. Zero means 60 seconds.
	ClientTimeout time.Duration
	// DisableRetryOnRateLimit disables automatic 429/503 retry. When true,
	// rate-limit errors are returned immediately so the caller can decide.
	DisableRetryOnRateLimit bool
	// BlockPrivateIPs attaches an SSRF-safe dialer that re-validates resolved IPs
	// at connect time, preventing DNS rebinding attacks. Set for runtime-added servers.
	BlockPrivateIPs bool
	ServerName      string
	// Nil means static Headers are applied without dynamic refresh.
	AuthProvider AuthorizationProvider
	// Empty defaults to "Authorization".
	AuthHeaderName string
}

func NewHTTPConnection(cfg HTTPConnectionConfig) (*HTTPConnection, error) {
	if cfg.Clock == nil {
		return nil, fmt.Errorf("HTTPConnectionConfig.Clock is required")
	}
	listenerCtx, listenerCancel := context.WithCancel(context.Background())
	return &HTTPConnection{
		url:                     cfg.URL,
		serverName:              cfg.ServerName,
		headers:                 cfg.Headers,
		authProvider:            cfg.AuthProvider,
		authHeaderName:          cfg.AuthHeaderName,
		disableRetryOnRateLimit: cfg.DisableRetryOnRateLimit,
		client:                  noRedirectClient(resolveClientTimeout(cfg.ClientTimeout), cfg.BlockPrivateIPs),
		clock:                   cfg.Clock,
		listenerCtx:             listenerCtx,
		listenerCancel:          listenerCancel,
	}, nil
}

func resolveClientTimeout(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return defaultHTTPClientTimeout
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
	return c.postWithAuthRetry(ctx, req)
}

// Each c.post() call gets its own rate-limit retry budget so a 401 replay
// cannot multiply 429/503 retries.
func (c *HTTPConnection) postWithAuthRetry(ctx context.Context, rpcReq Request) (json.RawMessage, error) {
	body, err := c.post(ctx, rpcReq)
	if c.authProvider == nil || !isUnauthorized(err) {
		return body, err
	}
	if _, refreshErr := c.authProvider.RefreshAuthorization(ctx); refreshErr != nil {
		return nil, c.authRemedyError(refreshErr)
	}
	body, err = c.post(ctx, rpcReq)
	if isUnauthorized(err) {
		return nil, c.authRemedyError(err)
	}
	return body, err
}

func isUnauthorized(err error) bool {
	var uerr *UnauthorizedError
	return errors.As(err, &uerr)
}

func (c *HTTPConnection) authRemedyError(cause error) error {
	return fmt.Errorf("%s requires re-authorization; run `mini auth %s`: %w", c.serverName, c.serverName, cause)
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
		return postResult{}, &ConnectionError{Err: fmt.Errorf("http %s: %w", rpcReq.Method, err)}
	}
	defer resp.Body.Close()
	return c.processResponse(resp, rpcReq)
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
	if err := c.setRequestHeaders(ctx, httpReq); err != nil {
		return nil, err
	}
	return httpReq, nil
}

func (c *HTTPConnection) processResponse(resp *http.Response, request Request) (postResult, error) {
	c.storeSessionID(resp.Header.Get("Mcp-Session-Id"))
	if resp.StatusCode >= 400 {
		return c.httpErrorResult(resp, request.Method)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return postResult{}, fmt.Errorf("read response: %w", err)
	}
	result, err := c.parsePostBody(respBody, request.ID)
	return postResult{body: result}, err
}

func (c *HTTPConnection) parsePostBody(body []byte, requestID any) (json.RawMessage, error) {
	messages, err := splitHTTPMessages(body)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		var rpc struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if err := json.Unmarshal(message, &rpc); err != nil {
			return nil, fmt.Errorf("parse response: %w: %s", err, message[:min(200, len(message))])
		}
		if len(rpc.ID) == 0 && rpc.Method != "" {
			c.dispatchNotification(Notification{JSONRPC: "2.0", Method: rpc.Method, Params: rpc.Params})
			continue
		}
		if !sameJSONID(rpc.ID, requestID) {
			continue
		}
		if rpc.Error != nil {
			return nil, rpc.Error
		}
		return rpc.Result, nil
	}
	return nil, fmt.Errorf("response for request id %v not found", requestID)
}

func sameJSONID(raw json.RawMessage, id any) bool {
	want, err := json.Marshal(id)
	return err == nil && bytes.Equal(bytes.TrimSpace(raw), want)
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
	return postResult{}, nonRetryableHTTPError(resp, method, errBody)
}

func nonRetryableHTTPError(resp *http.Response, method string, errBody []byte) error {
	if resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("http %s: status %d: %s", method, resp.StatusCode, errBody)
	}
	return fmt.Errorf("http %s: status %d: %w", method, resp.StatusCode,
		&UnauthorizedError{WWWAuthenticate: resp.Header.Get("WWW-Authenticate")})
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

func (c *HTTPConnection) setRequestHeaders(ctx context.Context, req *http.Request) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Spec (Streamable HTTP): client MUST include MCP-Protocol-Version on all
	// post-initialize requests so the server can reject version mismatches.
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	req.Header.Set("X-Mini-Version", version.Version)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if err := c.applyAuthProvider(ctx, req); err != nil {
		return err
	}
	c.mu.Lock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Unlock()
	return nil
}

func (c *HTTPConnection) applyAuthProvider(ctx context.Context, req *http.Request) error {
	if c.authProvider == nil {
		return nil
	}
	value, err := c.authProvider.Authorization(ctx)
	if err != nil {
		return c.authRemedyError(err)
	}
	req.Header.Set(c.authHeaderNameOrDefault(), value)
	return nil
}

func (c *HTTPConnection) authHeaderNameOrDefault() string {
	if c.authHeaderName == "" {
		return "Authorization"
	}
	return c.authHeaderName
}

func (c *HTTPConnection) sleepCtx(ctx context.Context, d time.Duration) bool {
	t := c.clock.NewTimer(d)
	select {
	case <-ctx.Done():
		t.Stop()
		return false
	case <-t.Chan():
		return true
	}
}

func (c *HTTPConnection) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	if err := c.setRequestHeaders(ctx, req); err != nil {
		return err
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
