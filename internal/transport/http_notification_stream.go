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
)

const modernListenMethod = "subscriptions/listen"

var (
	errUnsupportedListenFilter = errors.New("server did not acknowledge tools-list changes")
	errListenComplete          = errors.New("subscription completed")
)

func (c *HTTPConnection) consumeNotificationStream(ctx context.Context, state *listenerState) (int, error) {
	modern := c.isModern()
	build := c.buildStreamRequest
	if modern {
		var err error
		build, err = c.buildModernListenRequest()
		if err != nil {
			return 0, err
		}
	}
	resp, err := c.sendOneWithAuthRetry(ctx, c.newStreamClient(), build)
	if err != nil {
		return 0, err
	}
	//nolint:errcheck // status and scan results determine handling; Close releases the response stream.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("notification stream status %d", resp.StatusCode)
	}
	if modern {
		return resp.StatusCode, c.scanSubscriptionStream(resp.Body, state)
	}
	return resp.StatusCode, c.scanNotificationStream(resp.Body)
}

type listenNotifications struct {
	ToolsListChanged bool `json:"toolsListChanged"`
}

type listenParams struct {
	Notifications listenNotifications `json:"notifications"`
}

type acknowledgedParams struct {
	Notifications listenNotifications `json:"notifications"`
}

func (c *HTTPConnection) buildModernListenRequest() (
	func(context.Context) (*http.Request, string, error), error,
) {
	params, err := json.Marshal(listenParams{Notifications: listenNotifications{ToolsListChanged: true}})
	if err != nil {
		return nil, fmt.Errorf("encode subscriptions/listen params: %w", err)
	}
	prepared, err := c.prepareModernParams(params)
	if err != nil {
		return nil, err
	}
	request := Request{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: modernListenMethod, Params: prepared}
	return c.modernListenRequestBuilder(request), nil
}

func (c *HTTPConnection) modernListenRequestBuilder(
	request Request,
) func(context.Context) (*http.Request, string, error) {
	return func(ctx context.Context) (*http.Request, string, error) {
		body, err := json.Marshal(request)
		if err != nil {
			return nil, "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
		if err != nil {
			return nil, "", err
		}
		sentAuth, err := c.setModernRequestHeaders(ctx, req, modernListenMethod)
		return req, sentAuth, err
	}
}

func (c *HTTPConnection) scanSubscriptionStream(body io.Reader, state *listenerState) error {
	return ScanSSEMessages(body, func(message json.RawMessage) error {
		var notification Notification
		if json.Unmarshal(message, &notification) == nil && notification.Method != "" {
			return c.handleSubscriptionNotification(notification, state)
		}
		var response Response
		if json.Unmarshal(message, &response) == nil && response.ID != nil && len(response.Result) != 0 {
			return errListenComplete
		}
		return nil
	})
}

func (c *HTTPConnection) handleSubscriptionNotification(
	notification Notification,
	state *listenerState,
) error {
	if notification.Method != "notifications/subscriptions/acknowledged" {
		c.dispatchNotification(notification)
		return nil
	}
	var ack acknowledgedParams
	if json.Unmarshal(notification.Params, &ack) != nil || !ack.Notifications.ToolsListChanged {
		return errUnsupportedListenFilter
	}
	slog.Debug("upstream acknowledged notification subscription", "url", c.url)
	if state.refreshOnAck {
		c.toolsChanged.NotifyToolsChanged()
	}
	state.refreshOnAck = false
	return nil
}

func (c *HTTPConnection) buildStreamRequest(ctx context.Context) (*http.Request, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, "", err
	}
	sentAuth, err := c.setRequestHeaders(ctx, req)
	if err != nil {
		return nil, "", err
	}
	c.attachSessionID(req)
	req.Header.Del("Content-Type")
	req.Header.Set("Accept", "text/event-stream")
	return req, sentAuth, nil
}

func (c *HTTPConnection) scanNotificationStream(body io.Reader) error {
	return ScanSSEMessages(body, func(message json.RawMessage) error {
		var notification Notification
		if json.Unmarshal(message, &notification) == nil && notification.Method != "" {
			c.dispatchNotification(notification)
		}
		return nil
	})
}
