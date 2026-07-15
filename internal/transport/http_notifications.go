package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mcpmini/mini/internal/version"
)

func (c *HTTPConnection) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	return paginateToolsList(ctx, c.callToolsPage)
}

func (c *HTTPConnection) ensureInitialized(ctx context.Context) error {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.initialized {
		return nil
	}
	if err := c.initHandshake(ctx); err != nil {
		return err
	}
	c.initialized = true
	return nil
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
	result, err := c.sendInitialize(ctx)
	if err != nil {
		return err
	}
	if err := c.sendInitializedNotification(ctx); err != nil {
		return err
	}
	if toolsListChanged(result.Capabilities) {
		c.listenerWG.Add(1)
		go c.listenForNotifications()
	}
	return nil
}

func (c *HTTPConnection) sendInitialize(ctx context.Context) (InitializeResult, error) {
	params, _ := json.Marshal(InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mini", Version: version.Version},
	})
	raw, err := c.rpc(ctx, "initialize", params)
	if err != nil {
		return InitializeResult{}, err
	}
	return parseInitializeResult(raw)
}

func toolsListChanged(capabilities map[string]any) bool {
	tools, ok := capabilities["tools"].(map[string]any)
	if !ok {
		return false
	}
	changed, _ := tools["listChanged"].(bool)
	return changed
}

func (c *HTTPConnection) sendInitializedNotification(ctx context.Context) error {
	notif, _ := json.Marshal(Notification{JSONRPC: "2.0", Method: NotificationInitialized})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(notif))
	if err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	if err := c.setRequestHeaders(ctx, httpReq); err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (c *HTTPConnection) SetNotificationHandler(handler func(Notification)) {
	c.toolsChanged.SetHandler(handler)
}

func (c *HTTPConnection) dispatchNotification(notification Notification) {
	if notification.Method == NotificationToolsChanged {
		c.toolsChanged.NotifyToolsChanged()
		return
	}
	if handler := c.toolsChanged.Handler(); handler != nil {
		handler(notification)
	}
}

func (c *HTTPConnection) listenForNotifications() {
	defer c.listenerWG.Done()
	for c.listenerCtx.Err() == nil {
		status, err := c.consumeNotificationStream()
		if status == http.StatusMethodNotAllowed || c.listenerCtx.Err() != nil {
			if status == http.StatusMethodNotAllowed {
				slog.Warn("upstream advertises tool changes but rejects notification stream", "url", c.url)
			}
			return
		}
		if err != nil {
			slog.Warn("upstream notification stream interrupted", "url", c.url, "err", err)
		}
		if !c.sleepCtx(c.listenerCtx, time.Second) {
			return
		}
	}
}

func (c *HTTPConnection) consumeNotificationStream() (int, error) {
	req, err := c.newNotificationStreamRequest()
	if err != nil {
		return 0, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("notification stream status %d", resp.StatusCode)
	}
	return resp.StatusCode, c.scanNotificationStream(resp.Body)
}

func (c *HTTPConnection) newNotificationStreamRequest() (*http.Request, error) {
	req, err := http.NewRequestWithContext(c.listenerCtx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	if err := c.setRequestHeaders(c.listenerCtx, req); err != nil {
		return nil, err
	}
	req.Header.Del("Content-Type")
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
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

func (c *HTTPConnection) Close() error {
	c.listenerCancel()
	c.listenerWG.Wait()
	return nil
}
