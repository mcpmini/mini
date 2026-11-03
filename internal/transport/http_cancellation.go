package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"

	"github.com/mcpmini/mini/internal/clock"
)

type postAttempt struct {
	req     *http.Request
	call    httpCall
	written atomic.Bool
}

func newPostAttempt(req *http.Request, call httpCall) *postAttempt {
	attempt := &postAttempt{call: call}
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		attempt.written.Store(info.Err == nil)
	}}
	attempt.req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	return attempt
}

func (c *HTTPConnection) cancelIfAbandoned(ctx context.Context, attempt *postAttempt, err error) {
	// A modern upstream treats the closed response stream itself as the cancellation:
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/ab3a39c13bd23be691c2760e1c6c5c15a64582e1/docs/specification/2026-07-28/basic/transports/streamable-http.mdx#L233-L238
	if attempt.call.modernVersion != "" || !methodAcceptsCancellation(attempt.call.Method) ||
		!attempt.written.Load() || !abandonedWhileWaiting(ctx, err) {
		return
	}
	noticeCtx, cancel := clock.WithTimeout(context.WithoutCancel(ctx), c.clock, upstreamCancelTimeout)
	defer cancel()
	if err := c.postCancellation(noticeCtx, attempt.req, attempt.call.ID); err != nil {
		slog.Debug("send upstream cancellation", "err", err)
	}
}

func abandonedWhileWaiting(ctx context.Context, err error) bool {
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func (c *HTTPConnection) postCancellation(ctx context.Context, original *http.Request, id any) error {
	req, err := buildCancellationRequest(ctx, original, id)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // Best-effort notification; no response data is consumed.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cancellation: HTTP status %d", resp.StatusCode)
	}
	return nil
}

func buildCancellationRequest(ctx context.Context, original *http.Request, id any) (*http.Request, error) {
	rawID, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	notification, err := cancelledNotification(rawID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(notification)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, original.URL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = original.Header.Clone()
	return req, nil
}
