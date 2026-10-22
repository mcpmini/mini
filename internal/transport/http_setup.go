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

func (c *HTTPConnection) withSessionRecovery(
	ctx context.Context,
	do func() (json.RawMessage, error),
) (json.RawMessage, error) {
	result, err := do()
	var expiredErr *SessionExpiredError
	if !errors.As(err, &expiredErr) {
		return result, err
	}
	c.compareAndResetSession(expiredErr.SessionID)
	if _, initErr := c.ensureReady(ctx); initErr != nil {
		return nil, initErr
	}
	return do()
}

func (c *HTTPConnection) compareAndResetSession(expiredID string) {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	c.mu.Lock()
	match := c.sessionID == expiredID
	c.mu.Unlock()
	if match {
		c.initialized = false
	}
}

func (c *HTTPConnection) negotiatedModernVersion() string {
	if v := c.modernVersion.Load(); v != nil {
		return *v
	}
	return ""
}

func (c *HTTPConnection) isModern() bool {
	return c.negotiatedModernVersion() != ""
}

func (c *HTTPConnection) resetModernEra() {
	c.initMu.Lock()
	c.eraKnown = false
	c.modernVersion.Store(nil)
	c.initMu.Unlock()
}

func setDiscoverRequestHeaders(req *http.Request) {
	req.Header.Set("MCP-Protocol-Version", ModernVersions[0])
	req.Header.Set("Mcp-Method", "server/discover")
}

func (c *HTTPConnection) probeOnce(ctx context.Context) (modernVersion string, err error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.eraKnown {
		return c.negotiatedModernVersion(), nil
	}
	result, err := c.probe(ctx)
	verdict, err := classifyDiscover(result, err)
	if err != nil {
		return "", err
	}
	c.eraKnown = true
	if verdict.era == EraModern {
		c.modernVersion.Store(&verdict.version)
		slog.Info("upstream supports MCP 2026-07-28", "server", c.serverName)
	}
	if verdict.listen {
		//nolint:contextcheck // Listener lifetime belongs to the connection, not the probe request.
		c.startListener()
	}
	return verdict.version, nil
}

func (c *HTTPConnection) probe(ctx context.Context) (json.RawMessage, error) {
	params, err := prepareModernParams(nil, ModernVersions[0])
	if err != nil {
		return nil, err
	}
	return c.rpc(ctx, "server/discover", params)
}

func classifyDiscover(result json.RawMessage, err error) (probeVerdict, error) {
	if err == nil {
		return classifyDiscoverResult(result)
	}
	return classifyDiscoverError(err)
}

func classifyDiscoverError(err error) (probeVerdict, error) {
	// The spec reads anything other than a recognized modern answer as legacy, including a reply that answers nothing.
	var unusable unusableResponseError
	if errors.As(err, &unusable) {
		return probeVerdict{era: EraLegacy}, nil
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return classifyDiscoverRPC(rpcErr)
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		if statusErr.RPC != nil {
			return classifyDiscoverRPC(statusErr.RPC)
		}
		if isLegacyProbeStatus(statusErr.StatusCode) {
			return probeVerdict{era: EraLegacy}, nil
		}
	}
	return probeVerdict{}, err
}

func classifyDiscoverResult(result json.RawMessage) (probeVerdict, error) {
	versions, recognized := discoverSupportedVersions(result)
	if !recognized {
		return legacyProbeVerdict()
	}
	verdict := modernVersionVerdict(versions)
	if verdict.era != EraModern {
		return verdict, nil
	}
	if err := requireCompleteResult(result, "server/discover"); err != nil {
		return probeVerdict{}, err
	}
	var discovered DiscoverResult
	if err := json.Unmarshal(result, &discovered); err != nil {
		return legacyProbeVerdict()
	}
	verdict.listen = verdict.era == EraModern && discovered.Capabilities.ToolsListChanged()
	return verdict, nil
}

func classifyDiscoverRPC(err *RPCError) (probeVerdict, error) {
	if isModernRejection(err.Code) {
		return probeVerdict{}, err
	}
	if err.Code != CodeUnsupportedProtocolVersion {
		return probeVerdict{era: EraLegacy}, nil
	}
	data, ok := decodeUnsupportedVersionData(err.Data)
	if !ok {
		return probeVerdict{era: EraLegacy}, nil
	}
	// The spec says not to fall back after -32022, but servers that list only legacy versions here
	// (Atlassian v2) still accept initialize.
	return modernVersionVerdict(data.Supported), nil
}

func isLegacyProbeStatus(status int) bool {
	return status >= 400 && status < 500 && status != 403 && status != 408 && status != 429
}
