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
	if toolsListChanged(result.Capabilities) && !c.listenerStarted {
		c.listenerStarted = true
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
	resp, err := c.sendOneWithAuthRetry(ctx, c.client, c.buildInitializedNotifRequest(notif))
	if err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("notifications/initialized: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

func (c *HTTPConnection) buildInitializedNotifRequest(notif []byte) func(context.Context) (*http.Request, string, error) {
	return func(ctx context.Context) (*http.Request, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(notif))
		if err != nil {
			return nil, "", err
		}
		sentAuth, err := c.setRequestHeaders(ctx, req)
		return req, sentAuth, err
	}
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

const maxListenerBackoff = 60 * time.Second

type listenerState struct {
	backoff       time.Duration
	rejectedAuth  string
	lastSessionID string
}

func (c *HTTPConnection) loadSessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

func (c *HTTPConnection) listenForNotifications() {
	defer c.listenerWG.Done()
	state := listenerState{backoff: time.Second}
	for c.listenerCtx.Err() == nil {
		if state.rejectedAuth != "" && c.currentAuth(c.listenerCtx) == state.rejectedAuth {
			if !c.sleepCtx(c.listenerCtx, maxListenerBackoff) {
				return
			}
			continue
		}
		if c.runListenerCycle(&state) {
			return
		}
	}
}

func (c *HTTPConnection) runListenerCycle(state *listenerState) (stop bool) {
	sidAtStart := c.loadSessionID()
	status, err := c.consumeNotificationStream()
	if c.shouldStopListener(status) {
		return true
	}
	if err != nil {
		slog.Warn("upstream notification stream interrupted", "url", c.url, "err", err)
	}
	state.rejectedAuth = c.rejectedAuthAfter(c.listenerCtx, err)
	state.resetBackoffIfReinitialized(sidAtStart)
	delay, next := listenerDelay(status, err, state.backoff)
	state.backoff = next
	return !c.sleepCtx(c.listenerCtx, delay)
}

func (c *HTTPConnection) currentAuth(ctx context.Context) string {
	if c.authProvider == nil {
		return ""
	}
	v, _ := c.authProvider.Authorization(ctx)
	return v
}

func (c *HTTPConnection) shouldStopListener(status int) bool {
	if status == http.StatusMethodNotAllowed {
		slog.Warn("upstream advertises tool changes but rejects notification stream", "url", c.url)
		return true
	}
	return c.listenerCtx.Err() != nil
}

func (c *HTTPConnection) rejectedAuthAfter(ctx context.Context, err error) string {
	// A failed refresh never sent a credential, so only an upstream 401 marks the current one rejected.
	if errors.Is(err, ErrReauthRequired) && isUnauthorized(err) {
		return c.currentAuth(ctx)
	}
	return ""
}

func listenerDelay(status int, err error, backoff time.Duration) (delay, next time.Duration) {
	if status == http.StatusOK {
		return time.Second, time.Second
	}
	if errors.Is(err, ErrReauthRequired) {
		return maxListenerBackoff, maxListenerBackoff
	}
	return backoff, min(backoff*2, maxListenerBackoff)
}

func (c *HTTPConnection) consumeNotificationStream() (int, error) {
	resp, err := c.sendOneWithAuthRetry(c.listenerCtx, c.newStreamClient(), c.buildStreamRequest)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("notification stream status %d", resp.StatusCode)
	}
	return resp.StatusCode, c.scanNotificationStream(resp.Body)
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

func (c *HTTPConnection) Close() error {
	c.listenerCancel()
	c.listenerWG.Wait()
	return nil
}

func (s *listenerState) resetBackoffIfReinitialized(sessionID string) {
	if s.lastSessionID != "" && sessionID != s.lastSessionID {
		s.backoff = time.Second
	}
	s.lastSessionID = sessionID
}
