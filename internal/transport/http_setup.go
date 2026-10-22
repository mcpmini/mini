package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func (c *HTTPConnection) ensureReady(ctx context.Context) (modernVersion string, err error) {
	modernVersion, err = c.probeOnce(ctx)
	if err != nil || modernVersion != "" {
		return modernVersion, err
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
	return "", err
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
	notif, _ := json.Marshal(Notification{JsonRPC: JsonRPCVersion, Method: NotificationInitialized})
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

func (c *HTTPConnection) Close() error {
	c.listenerMu.Lock()
	c.closed = true
	c.listenerMu.Unlock()
	c.listenerCancel()
	c.listenerWG.Wait()
	return nil
}
