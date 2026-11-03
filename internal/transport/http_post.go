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
	"time"

	"github.com/mcpmini/mini/internal/version"
)

const maxRetries = 3

type postResult struct {
	body      json.RawMessage
	retryable bool
	delay     time.Duration // -1 = no Retry-After header; caller uses backoff
	sentAuth  string
}

func (c *HTTPConnection) post(ctx context.Context, call httpCall) (postResult, error) {
	backoff := time.Second
	for i := range maxRetries {
		result, done, err := c.postWithRetryDelay(postRetryDelayParams{
			ctx: ctx, call: call, attempt: i, backoff: &backoff,
		})
		if done {
			return result, err
		}
	}
	return postResult{}, fmt.Errorf("exceeded max retries for %s", call.Method)
}

type postRetryDelayParams struct {
	ctx     context.Context
	call    httpCall
	attempt int
	backoff *time.Duration
}

func (c *HTTPConnection) postWithRetryDelay(p postRetryDelayParams) (postResult, bool, error) {
	r, err := c.doPost(p.ctx, p.call)
	if err == nil {
		return r, true, nil
	}
	if shouldStopRetrying(r, p.attempt, c.disableRetryOnRateLimit) {
		return r, true, err
	}
	delay := nextRetryDelay(r.delay, p.backoff)
	slog.Warn("upstream http request retrying", "method", p.call.Method, "attempt", p.attempt+1, "delay", delay)
	if !c.sleepCtx(p.ctx, delay) {
		return postResult{}, true, p.ctx.Err()
	}
	return postResult{}, false, nil
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

func (c *HTTPConnection) doPost(ctx context.Context, call httpCall) (postResult, error) {
	httpReq, sentAuth, err := c.buildHTTPRequest(ctx, call)
	if err != nil {
		return postResult{}, err
	}
	sentSessionID := httpReq.Header.Get("Mcp-Session-Id")
	attempt := newPostAttempt(httpReq, call)
	resp, err := c.client.Do(attempt.req)
	if err != nil {
		c.cancelIfAbandoned(ctx, attempt, err)
		return postResult{sentAuth: sentAuth}, &ConnectionError{Err: fmt.Errorf("http %s: %w", call.Method, err)}
	}
	c.observeAuthorizationChallenge(resp)
	//nolint:errcheck // processResponse determines the result; Close releases the response stream.
	defer resp.Body.Close()
	result, err := c.processResponse(resp, call.Request, sentSessionID)
	result.sentAuth = sentAuth
	c.cancelIfAbandoned(ctx, attempt, err)
	return result, err
}

func (c *HTTPConnection) observeAuthorizationChallenge(resp *http.Response) {
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		return
	}
	observer, ok := c.authProvider.(AuthorizationChallengeObserver)
	if !ok {
		return
	}
	observer.ObserveAuthorizationChallenge(AuthorizationChallenge{
		StatusCode: resp.StatusCode, WWWAuthenticate: wwwAuthenticate(resp.Header),
	})
}

func (c *HTTPConnection) buildHTTPRequest(ctx context.Context, call httpCall) (*http.Request, string, error) {
	if call.modernVersion != "" {
		return c.buildModernHTTPRequest(ctx, call)
	}
	reqBody, err := json.Marshal(call.Request)
	if err != nil {
		return nil, "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, "", err
	}
	sentAuth, err := c.setRequestHeaders(ctx, httpReq)
	if err != nil {
		return nil, "", err
	}
	// A strict legacy server can reject an unsupported version header before initialize
	// negotiates a version both sides support:
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/ab3a39c13bd23be691c2760e1c6c5c15a64582e1/docs/specification/2025-11-25/basic/transports.mdx#L279-L280
	if call.Method == "initialize" {
		httpReq.Header.Del("MCP-Protocol-Version")
	}
	if call.Method == "server/discover" {
		setDiscoverRequestHeaders(httpReq)
	}
	// The stored ID may belong to the session being replaced, and a new session must start
	// with an InitializeRequest that carries no session ID:
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/ab3a39c13bd23be691c2760e1c6c5c15a64582e1/docs/specification/2025-11-25/basic/transports.mdx#L213-L215
	if call.Method != "initialize" && call.Method != "server/discover" {
		c.attachSessionID(httpReq)
	}
	return httpReq, sentAuth, nil
}

func (c *HTTPConnection) buildModernHTTPRequest(ctx context.Context, call httpCall) (*http.Request, string, error) {
	req := call.Request
	body, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	sentAuth, err := c.setRequestHeaders(ctx, httpReq)
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("MCP-Protocol-Version", call.modernVersion)
	httpReq.Header.Set("Mcp-Method", req.Method)
	if req.Method == "tools/call" {
		c.addToolCallHeaders(httpReq, req.Params)
	}
	return httpReq, sentAuth, nil
}

func (c *HTTPConnection) processResponse(
	resp *http.Response,
	request Request,
	sentSessionID string,
) (postResult, error) {
	if resp.StatusCode >= 400 {
		return c.httpErrorResult(resp, request.Method, sentSessionID)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return postResult{}, fmt.Errorf("read response: %w", err)
	}
	result, err := c.parsePostBody(respBody, request.ID)
	if err == nil && request.Method == "initialize" {
		c.storeSessionID(resp.Header.Get("Mcp-Session-Id"))
	}
	return postResult{body: result}, err
}

func (c *HTTPConnection) storeSessionID(sessionID string) {
	if len(sessionID) > 256 {
		return
	}
	c.sessionMu.Lock()
	c.sessionID = sessionID
	c.sessionMu.Unlock()
}

func (c *HTTPConnection) httpErrorResult(resp *http.Response, method string, sentSessionID string) (postResult, error) {
	//nolint:errcheck // bounded body text is diagnostic; HTTP status remains the failure result.
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if isRetryableStatus(resp.StatusCode) {
		return postResult{
			retryable: true,
			delay:     parseRetryAfter(resp.Header.Get("Retry-After"), c.clock.Now()),
		}, fmt.Errorf("http %s: status %d: %s", method, resp.StatusCode, errBody)
	}
	if resp.StatusCode == http.StatusNotFound && sentSessionID != "" {
		return postResult{}, &SessionExpiredError{SessionID: sentSessionID}
	}
	return postResult{}, nonRetryableHTTPError(resp, method, errBody)
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

func (c *HTTPConnection) setRequestHeaders(ctx context.Context, req *http.Request) (string, error) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	protocolVersion := LatestLegacyVersion
	c.sessionMu.Lock()
	if c.protocolVersion != "" {
		protocolVersion = c.protocolVersion
	}
	c.sessionMu.Unlock()
	req.Header.Set("X-Mini-Version", version.Version)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	sentAuth, err := c.applyAuthProvider(ctx, req)
	if err != nil {
		return "", err
	}
	return sentAuth, nil
}

func (c *HTTPConnection) attachSessionID(req *http.Request) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
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
