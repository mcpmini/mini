package transport

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"
)

type sessionListener struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (l *sessionListener) stopped() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
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
	// A listener that stopped for good (say the upstream refused subscriptions/listen) is replaced:
	// discovery advertising tool changes again is the upstream saying it can now send them.
	if c.listener != nil && !c.listener.stopped() {
		return
	}
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return
	}
	if c.listener != nil {
		c.listener.cancel()
	}
	listenerCtx, cancel := context.WithCancel(c.listenerCtx)
	c.listener = &sessionListener{cancel: cancel, done: make(chan struct{})}
	c.listenerWG.Add(1)
	go c.listenForNotifications(listenerCtx, c.listener.done, c.isModern())
}

func (c *HTTPConnection) SetNotificationHandler(handler func(Notification)) {
	c.toolsChanged.SetHandler(handler)
}

func (c *HTTPConnection) handleNotification(notification Notification) {
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
	// Fixed for the listener's life: a legacy upstream gets a new listener from its own handshake.
	modern       bool
	backoff      time.Duration
	rejectedAuth string
}

func (c *HTTPConnection) listenForNotifications(ctx context.Context, done chan struct{}, modern bool) {
	defer c.listenerWG.Done()
	defer close(done)
	state := listenerState{modern: modern, backoff: time.Second}
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
	if state.modern && !c.isModern() {
		// The era was reset; listen again once the next call's probe settles it.
		return !c.sleepCtx(ctx, time.Second)
	}
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

func (c *HTTPConnection) buildModernListenRequest() (
	func(context.Context) (*http.Request, string, error), error,
) {
	modernVersion := cmp.Or(c.negotiatedModernVersion(), ModernVersions[0])
	prepared, err := modernListenParams(modernVersion)
	if err != nil {
		return nil, err
	}
	request := Request{
		JsonRPC: JsonRPCVersion,
		ID:      c.nextID.Add(1),
		Method:  MethodSubscriptionsListen,
		Params:  prepared,
	}
	return func(ctx context.Context) (*http.Request, string, error) {
		return c.buildModernHTTPRequest(ctx, httpCall{Request: request, modernVersion: modernVersion})
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
	if notification.Method != NotificationSubscriptionsAcknowledged {
		c.handleNotification(notification)
		return nil
	}
	var ack ListenParams
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
			c.handleNotification(notification)
		}
		return nil
	})
}
