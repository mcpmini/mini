package invoke

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type DialParams struct {
	Logger           *slog.Logger
	Config           *config.Config
	Server           config.ServerConfig
	Clock            clock.Clock
	ConfigDir        string
	ProviderRegistry *provider.Registry
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
	if p.ProviderRegistry == nil || !isOAuth2Server(p.Server) {
		return nil
	}
	// A hand-set header or auth.token means the user chose static auth; the provider would override it.
	if hasHeader(cfg.Headers, p.Server.Auth.HeaderName()) {
		return nil
	}
	params := provider.Params{
		AuthConfig: p.Server.Auth,
		ConfigDir:  p.ConfigDir,
		ServerName: p.Server.Name,
		ServerURL:  p.Server.URL,
		Clock:      p.Clock,
	}
	provider, err := p.ProviderRegistry.GetOrCreate(params)
	if err != nil {
		return fmt.Errorf("build auth provider for %s: %w", p.Server.Name, err)
	}
	cfg.AuthProvider = provider
	cfg.AuthHeaderName = p.Server.Auth.HeaderName()
	return nil
}

func hasHeader(headers map[string]string, name string) bool {
	for k, v := range headers {
		if strings.EqualFold(k, name) && v != "" {
			return true
		}
	}
	return false
}

func isOAuth2Server(sc config.ServerConfig) bool {
	return sc.Auth != nil && sc.Auth.Type == config.AuthTypeOAuth2
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
	if auth.Type == config.AuthTypeAPIKey {
		headers[auth.HeaderName()] = token
		return
	}
	headers[auth.HeaderName()] = "Bearer " + token
}

func parseClientTimeout(spec string) time.Duration {
	ts, err := config.ParseTimeoutSpec(spec, 0)
	if err != nil || !ts.Enabled {
		return 0
	}
	return ts.Duration
}
