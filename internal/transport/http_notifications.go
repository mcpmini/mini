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
)

func (c *HTTPConnection) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	tools, err := paginateToolsList(ctx, c.callToolsPage)
	if err != nil {
		return nil, err
	}
	if c.isModern() {
		return c.rebuildToolHeaders(tools), nil
	}
	return tools, nil
}

func (c *HTTPConnection) ensureReady(ctx context.Context) error {
	if err := c.probeOnce(ctx); err != nil {
		return err
	}
	if c.isModern() {
		return nil
	}
	renewed, err := c.initializeOnce(ctx)
	if renewed {
		// mini keeps a copy of this upstream's tool list and refreshes it when the upstream
		// sends notifications/tools/list_changed. That message never covers a restart:
		//  1. mini connects to the upstream in session s1 and fetches its tools.
		//  2. The upstream is redeployed with a different set of tools, which ends s1.
		//  3. mini's next request gets a 404, so mini starts session s2.
		//  4. To the upstream, s2 is a brand-new client, so it has no change to announce.
		// Without this notification, mini would keep serving the tool list from s1.
		c.toolsChanged.NotifyToolsChanged()
	}
	return err
}

func (c *HTTPConnection) initializeOnce(ctx context.Context) (renewed bool, err error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.initialized {
		return false, nil
	}
	if err := c.initHandshake(ctx); err != nil {
		return false, err
	}
	renewed = c.initializedBefore
	c.initialized, c.initializedBefore = true, true
	return renewed, nil
}

func (c *HTTPConnection) callToolsPage(ctx context.Context, cursor string) (ToolsListResult, error) {
	var params json.RawMessage
	if cursor != "" {
		//nolint:errcheck // cursor map contains only strings.
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
	c.mu.Lock()
	c.protocolVersion = result.ProtocolVersion
	c.mu.Unlock()
	if err := c.sendInitializedNotification(ctx); err != nil {
		return err
	}
	//nolint:contextcheck // The connection owns future notifications beyond the handshake caller's context.
	c.restartListener(toolsListChanged(result.Capabilities))
	return nil
}

type sessionListener struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (c *HTTPConnection) restartListener(listen bool) {
	// Each session negotiates its own capabilities, including whether it sends list-changed
	// notifications, so each session gets its own listener:
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/ab3a39c13bd23be691c2760e1c6c5c15a64582e1/docs/specification/2025-11-25/basic/lifecycle.mdx#L186-L187
	// The server MAY leave the old session's stream open, so the old listener is stopped here:
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/ab3a39c13bd23be691c2760e1c6c5c15a64582e1/docs/specification/2025-11-25/basic/transports.mdx#L150
	if old := c.listener; old != nil {
		old.cancel()
		<-old.done
		c.listener = nil
	}
	if !listen {
		return
	}
	c.startListener()
}

func (c *HTTPConnection) startListener() {
	if c.listener != nil {
		return
	}
	listenerCtx, cancel := context.WithCancel(c.listenerCtx)
	c.listener = &sessionListener{cancel: cancel, done: make(chan struct{})}
	c.listenerWG.Add(1)
	go c.listenForNotifications(listenerCtx, c.listener.done)
}

func (c *HTTPConnection) sendInitialize(ctx context.Context) (InitializeResult, error) {
	//nolint:errcheck // fixed handshake fields contain only JSON-serializable values.
	params, _ := json.Marshal(newInitializeParams())
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
	//nolint:errcheck // fixed notification fields are JSON-serializable.
	notif, _ := json.Marshal(Notification{JSONRPC: "2.0", Method: NotificationInitialized})
	resp, err := c.sendOneWithAuthRetry(ctx, c.client, c.buildInitializedNotifRequest(notif))
	if err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}
	//nolint:errcheck // status and body text determine the result; Close releases the response stream.
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		//nolint:errcheck // bounded body text is diagnostic; HTTP status remains the failure result.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("notifications/initialized: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

func (c *HTTPConnection) buildInitializedNotifRequest(
	notif []byte,
) func(context.Context) (*http.Request, string, error) {
	return func(ctx context.Context) (*http.Request, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(notif))
		if err != nil {
			return nil, "", err
		}
		sentAuth, err := c.setRequestHeaders(ctx, req)
		c.attachSessionID(req)
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
	backoff      time.Duration
	rejectedAuth string
	// The tool list mini holds may predate the subscription, so any acknowledgement that
	// follows a gap in coverage (startup or an ended attempt) re-fetches it.
	refreshOnAck bool
}

func (c *HTTPConnection) listenForNotifications(ctx context.Context, done chan struct{}) {
	defer c.listenerWG.Done()
	defer close(done)
	state := listenerState{backoff: time.Second, refreshOnAck: true}
	for ctx.Err() == nil {
		if state.rejectedAuth != "" && c.currentAuth(ctx) == state.rejectedAuth {
			if !c.sleepCtx(ctx, maxListenerBackoff) {
				return
			}
			continue
		}
		if c.runListenerCycle(ctx, &state) {
			return
		}
	}
}

func (c *HTTPConnection) runListenerCycle(ctx context.Context, state *listenerState) (stop bool) {
	status, err := c.consumeNotificationStream(ctx, state)
	if errors.Is(err, errUnsupportedListenFilter) {
		slog.Debug("upstream did not acknowledge tool-change subscription", "url", c.url)
		return true
	}
	if errors.Is(err, errListenComplete) {
		err = nil
	}
	if shouldStopListener(ctx, c.url, status) {
		return true
	}
	if err != nil {
		slog.Warn("upstream notification stream interrupted", "url", c.url, "err", err)
	}
	state.rejectedAuth = c.rejectedAuthAfter(ctx, err)
	state.refreshOnAck = true
	delay, next := listenerDelay(status, err, state.backoff)
	state.backoff = next
	return !c.sleepCtx(ctx, delay)
}

func (c *HTTPConnection) currentAuth(ctx context.Context) string {
	if c.authProvider == nil {
		return ""
	}
	//nolint:errcheck // this read fingerprints rejection; buildStreamRequest reports request credential errors.
	v, _ := c.authProvider.Authorization(ctx)
	return v
}

func shouldStopListener(ctx context.Context, url string, status int) bool {
	if status == http.StatusMethodNotAllowed {
		slog.Debug("upstream advertises tool changes but rejects notification stream", "url", url)
		return true
	}
	return ctx.Err() != nil
}

func (c *HTTPConnection) rejectedAuthAfter(ctx context.Context, err error) string {
	// A failed refresh never sent a credential, so only an upstream 401 marks the current one rejected.
	if errors.Is(err, ErrReauthRequired) && isUnauthorized(err) {
		return c.currentAuth(ctx)
	}
	return ""
}

func listenerDelay(status int, err error, backoff time.Duration) (delay, next time.Duration) {
	if status == http.StatusOK && !errors.Is(err, errListenRejected) {
		return time.Second, time.Second
	}
	if errors.Is(err, ErrReauthRequired) {
		return maxListenerBackoff, maxListenerBackoff
	}
	return backoff, min(backoff*2, maxListenerBackoff)
}

func (c *HTTPConnection) Close() error {
	c.listenerCancel()
	c.listenerWG.Wait()
	return nil
}
