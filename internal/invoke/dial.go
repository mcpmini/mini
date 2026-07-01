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

type DialParams struct {
	Logger *slog.Logger
	Config *config.Config
	Server config.ServerConfig
	Clock  clock.Clock
}

func Dial(ctx context.Context, p DialParams) (transport.Connection, error) {
	if p.Server.IsHTTPTransport() {
		return transport.NewHTTPConnection(transport.HTTPConnectionConfig{
			URL:                     p.Server.URL,
			Headers:                 MergedHeaders(p.Server),
			Clock:                   p.Clock,
			ClientTimeout:           parseClientTimeout(p.Server.HTTPClientTimeout),
			DisableRetryOnRateLimit: p.Server.DisableRetryOnRateLimit,
			BlockPrivateIPs:         p.Server.RuntimeAdded && !p.Config.DangerousAllowPrivateURLs,
		})
	}
	return transport.NewStdioConnection(ctx, transport.StdioCommand{Command: p.Server.Command, Args: p.Server.Args, Env: p.Server.Env, Logger: p.Logger})
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
