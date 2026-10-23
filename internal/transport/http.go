package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

// HTTPConnection implements Connection for streamable HTTP / SSE MCP servers.
// The GitHub MCP and similar servers use this transport: each call is a POST,
// responses may be SSE-wrapped, and a session ID is tracked across calls.
type HTTPConnection struct {
	url                     string
	serverName              string
	headers                 map[string]string
	authProvider            AuthorizationProvider
	authHeaderName          string
	disableRetryOnRateLimit bool
	client                  *http.Client
	clock                   clock.Clock
	nextID                  atomic.Int64
	sessionID               string
	protocolVersion         string
	mu                      sync.Mutex
	initMu                  sync.Mutex
	eraKnown                bool
	// Atomic because request building reads it while a probe may hold initMu.
	modernVersion     atomic.Pointer[string]
	modernMu          sync.RWMutex
	modernListMu      sync.Mutex
	modernHeaders     map[string][]headerParameter
	initialized       bool
	initializedBefore bool
	listener          *sessionListener
	listenerCtx       context.Context
	listenerCancel    context.CancelFunc
	listenerWG        sync.WaitGroup
	// Guards closed, so no listener is added to listenerWG once Close has started waiting on it.
	listenerMu   sync.Mutex
	closed       bool
	toolsChanged toolsChangedNotifier
}

// defaultHTTPClientTimeout is the hard network-level backstop. Set to 2× the default
// ToolTimeout (30s) so the per-call deadline fires first in normal operation and this
// only activates when context cancellation fails at the OS/network level. Users with
// slow tools (tool_timeout > 60s) should set http_client_timeout in their server YAML.
const defaultHTTPClientTimeout = 60 * time.Second

// SessionExpiredError is a 404 to a session-bearing request; MCP Streamable HTTP §Session Management requires re-initialize.
type SessionExpiredError struct {
	SessionID string
}

func (e *SessionExpiredError) Error() string {
	return fmt.Sprintf("MCP session expired (session %q)", e.SessionID)
}

// UnauthorizedError carries the WWW-Authenticate header from a 401 response so callers
// can distinguish an OAuth challenge (RFC 9728 §5.1) from a bare auth failure.
type UnauthorizedError struct {
	WWWAuthenticate string
}

func (e *UnauthorizedError) Error() string {
	return fmt.Sprintf("unauthorized (WWW-Authenticate: %q)", e.WWWAuthenticate)
}

type HTTPConnectionConfig struct {
	URL     string
	Headers map[string]string
	Clock   clock.Clock
	// ClientTimeout is the hard network-level deadline. Zero means 60 seconds.
	ClientTimeout time.Duration
	// DisableRetryOnRateLimit disables automatic 429/503 retry. When true,
	// rate-limit errors are returned immediately so the caller can decide.
	DisableRetryOnRateLimit bool
	// BlockPrivateIPs attaches an SSRF-safe dialer that re-validates resolved IPs
	// at connect time, preventing DNS rebinding attacks.
	BlockPrivateIPs bool
	ServerName      string
	AuthProvider    AuthorizationProvider
	AuthHeaderName  string
}

func NewHTTPConnection(cfg HTTPConnectionConfig) (*HTTPConnection, error) {
	if cfg.Clock == nil {
		return nil, fmt.Errorf("HTTPConnectionConfig.Clock is required")
	}
	if cfg.AuthProvider != nil && cfg.AuthHeaderName == "" {
		return nil, fmt.Errorf("HTTPConnectionConfig.AuthHeaderName is required when AuthProvider is set")
	}
	listenerCtx, listenerCancel := context.WithCancel(context.Background())
	return &HTTPConnection{
		url:                     cfg.URL,
		serverName:              cfg.ServerName,
		headers:                 cfg.Headers,
		authProvider:            cfg.AuthProvider,
		authHeaderName:          cfg.AuthHeaderName,
		disableRetryOnRateLimit: cfg.DisableRetryOnRateLimit,
		client: NewNoRedirectClient(
			NoRedirectClientOptions{
				Timeout:         resolveClientTimeout(cfg.ClientTimeout),
				BlockPrivateIPs: cfg.BlockPrivateIPs,
			},
		),
		clock:          cfg.Clock,
		listenerCtx:    listenerCtx,
		listenerCancel: listenerCancel,
	}, nil
}

func resolveClientTimeout(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return defaultHTTPClientTimeout
}

// No Timeout: http.Client.Timeout covers body reads and would cut an idle SSE stream.
func (c *HTTPConnection) newStreamClient() *http.Client {
	return &http.Client{CheckRedirect: c.client.CheckRedirect, Transport: c.client.Transport}
}

type NoRedirectClientOptions struct {
	Timeout         time.Duration
	BlockPrivateIPs bool
}

// NewNoRedirectClient blocks redirects to prevent session token exfiltration to a different host.
func NewNoRedirectClient(opts NoRedirectClientOptions) *http.Client {
	client := &http.Client{
		Timeout: opts.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if opts.BlockPrivateIPs {
		applySsrfTransport(client)
	}
	return client
}

func applySsrfTransport(client *http.Client) {
	dt, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		t := dt.Clone()
		t.DialContext = SSRFSafeDialer()
		t.Proxy = nil
		client.Transport = t
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

func (c *HTTPConnection) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if _, authErr := c.applyAuthProvider(ctx, req); authErr != nil {
		return authErr
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close() //nolint:errcheck // the server status is checked below; Close releases the response stream
	if resp.StatusCode >= 500 {
		return fmt.Errorf("server error: %d", resp.StatusCode)
	}
	return nil
}
