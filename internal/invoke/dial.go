package invoke

import (
	"context"
	"fmt"
	"log/slog"
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
	if p.Server.AgentAdded && !p.Config.DangerousAllowRuntimeStdio {
		return nil, fmt.Errorf("%s runs a command an agent added: set dangerous_allow_runtime_stdio to allow it, or delete agent_added from its file to trust it", p.Server.Name)
	}
	return transport.NewStdioConnection(ctx, transport.StdioCommand{Command: p.Server.Command, Args: p.Server.Args, Env: p.Server.Env, Logger: p.Logger})
}

func dialHTTP(p DialParams) (transport.Connection, error) {
	cfg := transport.HTTPConnectionConfig{
		URL:                     p.Server.URL,
		Headers:                 p.Server.MergedHeaders(),
		Clock:                   p.Clock,
		ClientTimeout:           parseClientTimeout(p.Server.HTTPClientTimeout),
		DisableRetryOnRateLimit: p.Server.DisableRetryOnRateLimit,
		BlockPrivateIPs:         p.Server.AgentAdded && !p.Config.DangerousAllowPrivateURLs,
		ServerName:              p.Server.Name,
	}
	if err := attachAuthProvider(&cfg, p); err != nil {
		return nil, err
	}
	return transport.NewHTTPConnection(cfg)
}

func attachAuthProvider(cfg *transport.HTTPConnectionConfig, p DialParams) error {
	// A hand-set header or auth.token means the user chose static auth; the provider would override it.
	if p.ProviderRegistry == nil || !p.Server.UsesOAuthLogin() {
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

func parseClientTimeout(spec string) time.Duration {
	ts, err := config.ParseTimeoutSpec(spec, 0)
	if err != nil || !ts.Enabled {
		return 0
	}
	return ts.Duration
}
