package invoke

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type DialParams struct {
	Logger    *slog.Logger
	Config    *config.Config
	Server    config.ServerConfig
	Clock     clock.Clock
	ConfigDir string
	// Only the long-lived serve paths set this; CLI commands inject headers
	// statically at startup.
	UseAuthProvider bool
	// ProviderCache, when non-nil, shares one AuthorizationProvider per server
	// across all dials (startup, reconnect, runtime-add). CLI one-shot paths
	// leave this nil and get a fresh provider per invocation.
	ProviderCache *auth.ProviderCache
}

func Dial(ctx context.Context, p DialParams) (transport.Connection, error) {
	if p.Server.IsHTTPTransport() {
		return dialHTTP(p)
	}
	return transport.NewStdioConnection(ctx, transport.StdioCommand{Command: p.Server.Command, Args: p.Server.Args, Env: p.Server.Env, Logger: p.Logger})
}

func dialHTTP(p DialParams) (transport.Connection, error) {
	cfg := transport.HTTPConnectionConfig{
		URL:                     p.Server.URL,
		Headers:                 MergedHeaders(p.Server),
		Clock:                   p.Clock,
		ClientTimeout:           parseClientTimeout(p.Server.HTTPClientTimeout),
		DisableRetryOnRateLimit: p.Server.DisableRetryOnRateLimit,
		BlockPrivateIPs:         p.Server.RuntimeAdded && !p.Config.DangerousAllowPrivateURLs,
		ServerName:              p.Server.Name,
	}
	if err := attachAuthProvider(&cfg, p); err != nil {
		return nil, err
	}
	return transport.NewHTTPConnection(cfg)
}

func attachAuthProvider(cfg *transport.HTTPConnectionConfig, p DialParams) error {
	if !p.UseAuthProvider || !isOAuth2Server(p.Server) {
		return nil
	}
	params := auth.ProviderParams{
		AuthConfig: p.Server.Auth,
		ConfigDir:  p.ConfigDir,
		ServerName: p.Server.Name,
		ServerURL:  p.Server.URL,
		Clock:      p.Clock,
	}
	provider, err := resolveProvider(params, p.ProviderCache)
	if err != nil {
		return fmt.Errorf("build auth provider for %s: %w", p.Server.Name, err)
	}
	cfg.AuthProvider = provider
	cfg.AuthHeaderName = authHeaderName(p.Server.Auth)
	return nil
}

func resolveProvider(params auth.ProviderParams, cache *auth.ProviderCache) (transport.AuthorizationProvider, error) {
	if cache != nil {
		return cache.GetOrCreate(params)
	}
	return auth.NewProvider(params)
}

func isOAuth2Server(sc config.ServerConfig) bool {
	return sc.Auth != nil && sc.Auth.Type == config.AuthTypeOAuth2
}

func authHeaderName(ac *config.AuthConfig) string {
	if ac.Header != "" {
		return ac.Header
	}
	return "Authorization"
}

// MergedHeaders returns the HTTP headers for sc, including injected auth.
func MergedHeaders(sc config.ServerConfig) map[string]string {
	headers := make(map[string]string)
	for k, v := range sc.Headers {
		headers[k] = strings.TrimSpace(os.Expand(v, os.Getenv))
	}
	if sc.Auth != nil {
		injectAuth(headers, sc.Auth)
	}
	return headers
}

func injectAuth(headers map[string]string, auth *config.AuthConfig) {
	token := strings.TrimSpace(os.Expand(auth.Token, os.Getenv))
	if token == "" {
		return
	}
	name := auth.Header
	if name == "" {
		name = "Authorization"
	}
	if auth.Type == config.AuthTypeAPIKey {
		headers[name] = token
		return
	}
	headers[name] = "Bearer " + token
}

func parseClientTimeout(spec string) time.Duration {
	d, enabled, err := config.ParseTimeoutSpec(spec, 0)
	if err != nil || !enabled {
		return 0
	}
	return d
}
