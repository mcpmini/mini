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

func (c *HTTPConnection) cancelAbandonedRequest(req *http.Request, call httpCall, err error) {
	ctxErr := req.Context().Err()
	if call.modernVersion != "" || call.Method == "initialize" || call.Method == "server/discover" ||
		ctxErr == nil || !errors.Is(err, ctxErr) {
		return
	}
	ctx, cancel := clock.WithTimeout(context.WithoutCancel(req.Context()), c.clock, upstreamCancelTimeout)
	defer cancel()
	if err := c.postCancellation(ctx, req, call.ID); err != nil {
		slog.Debug("send upstream cancellation", "err", err)
	}
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
	params, err := json.Marshal(CancelledParams{RequestID: rawID})
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(Notification{JsonRPC: JsonRPCVersion, Method: NotificationCancelled, Params: params})
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
