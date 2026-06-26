package invoke

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// Dial connects to an upstream MCP server, injecting auth headers.
func Dial(ctx context.Context, logger *slog.Logger, cfg *config.Config, sc config.ServerConfig, c clock.Clock) (transport.Connection, error) {
	switch sc.Transport {
	case "http", "sse", "streamable":
		return transport.NewHTTPConnection(transport.HTTPConnectionConfig{
			URL:                     sc.URL,
			Headers:                 MergedHeaders(sc),
			Clock:                   c,
			ClientTimeout:           parseClientTimeout(sc.HTTPClientTimeout),
			DisableRetryOnRateLimit: sc.DisableRetryOnRateLimit,
			BlockPrivateIPs:         sc.RuntimeAdded && !cfg.DangerousAllowPrivateURLs,
		})
	default:
		return transport.NewStdioConnection(ctx, transport.StdioCommand{Command: sc.Command, Args: sc.Args, Env: sc.Env, Logger: logger})
	}
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
	if auth.Type == "apikey" {
		headers[name] = token
		return
	}
	headers[name] = "Bearer " + token
}

func parseClientTimeout(spec string) time.Duration {
	if spec == "" || spec == "0" {
		return 0
	}
	d, err := time.ParseDuration(spec)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}
