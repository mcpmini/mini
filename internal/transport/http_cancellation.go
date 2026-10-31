package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/mcpmini/mini/internal/clock"
)

// A modern upstream treats the closed response stream itself as the cancellation:
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/ab3a39c13bd23be691c2760e1c6c5c15a64582e1/docs/specification/2026-07-28/basic/transports/streamable-http.mdx#L233-L238
func (c *HTTPConnection) cancelAbandonedRequest(req *http.Request, call httpCall, err error) {
	if call.modernVersion != "" || !methodAcceptsCancellation(call.Method) || !abandonedByCaller(req, err) {
		return
	}
	ctx, cancel := clock.WithTimeout(context.WithoutCancel(req.Context()), c.clock, upstreamCancelTimeout)
	defer cancel()
	if err := c.postCancellation(ctx, req, call.ID); err != nil {
		slog.Debug("send upstream cancellation", "err", err)
	}
}

func abandonedByCaller(req *http.Request, err error) bool {
	ctxErr := req.Context().Err()
	return ctxErr != nil && errors.Is(err, ctxErr)
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

// The notice reuses the original request's headers so it reaches the same session with the
// same credentials, even if a concurrent call has since rotated either.
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
