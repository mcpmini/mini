package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
)

const modernListenMethod = "subscriptions/listen"

var (
	errUnsupportedListenFilter = errors.New("server did not acknowledge tools-list changes")
	errListenComplete          = errors.New("subscription completed")
	errListenRejected          = errors.New("server rejected subscriptions/listen")
)

func (c *HTTPConnection) consumeNotificationStream(ctx context.Context, state *listenerState) (int, error) {
	modern := state.modern
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
		return resp.StatusCode, c.scanSubscriptionStream(resp)
	}
	return resp.StatusCode, c.scanNotificationStream(resp.Body)
}

type listenNotifications struct {
	ToolsListChanged bool `json:"toolsListChanged"`
}

type listenParams struct {
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
	return func(ctx context.Context) (*http.Request, string, error) {
		return c.buildModernHTTPRequest(ctx, request)
	}, nil
}

// A server may answer any POST with a single JSON body instead of an SSE stream:
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/8e12bf3c8f01f374146acfa19b8ece0018bfc86c/docs/specification/2026-07-28/basic/transports/streamable-http.mdx#L88-L91
func (c *HTTPConnection) scanSubscriptionStream(resp *http.Response) error {
	handle := c.handleSubscriptionMessage
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil &&
		mediaType == "application/json" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if err != nil {
			return err
		}
		return handle(body)
	}
	return ScanSSEMessages(resp.Body, handle)
}

func (c *HTTPConnection) handleSubscriptionMessage(message json.RawMessage) error {
	var notification Notification
	if json.Unmarshal(message, &notification) == nil && notification.Method != "" {
		return c.handleSubscriptionNotification(notification)
	}
	var response Response
	if json.Unmarshal(message, &response) == nil && response.ID != nil {
		return listenResponseOutcome(response)
	}
	return nil
}

func listenResponseOutcome(response Response) error {
	if response.Error != nil {
		return fmt.Errorf("%w: %w", errListenRejected, response.Error)
	}
	if len(response.Result) != 0 {
		return errListenComplete
	}
	return nil
}

func (c *HTTPConnection) handleSubscriptionNotification(notification Notification) error {
	if notification.Method != "notifications/subscriptions/acknowledged" {
		c.dispatchNotification(notification)
		return nil
	}
	var ack listenParams
	if json.Unmarshal(notification.Params, &ack) != nil || !ack.Notifications.ToolsListChanged {
		return errUnsupportedListenFilter
	}
	slog.Debug("upstream acknowledged notification subscription", "url", c.url)
	// Every stream follows a gap in coverage (startup or an ended attempt), so the tool list
	// mini holds may predate the subscription.
	c.toolsChanged.NotifyToolsChanged()
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
