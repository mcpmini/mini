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
	Logger *slog.Logger
	Config *config.Config
	Server config.ServerConfig
	Clock  clock.Clock
	// ConfigDir locates the token/registration store for auth providers.
	ConfigDir string
	// UseAuthProvider constructs a dynamic AuthorizationProvider for OAuth2
	// servers instead of relying on a statically-applied bearer header. Only
	// the serve paths (connect, daemon) set this; one-shot CLI commands dial
	// with static headers applied ahead of time via injectOAuthTokens.
	UseAuthProvider bool
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
	provider, err := auth.NewProvider(auth.ProviderParams{
		AuthConfig: p.Server.Auth,
		ConfigDir:  p.ConfigDir,
		ServerName: p.Server.Name,
		Clock:      p.Clock,
	})
	if err != nil {
		return fmt.Errorf("build auth provider for %s: %w", p.Server.Name, err)
	}
	cfg.AuthProvider = provider
	cfg.AuthHeaderName = authHeaderName(p.Server.Auth)
	return nil
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
