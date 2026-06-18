package invoke

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type DialParams struct {
	Logger *slog.Logger
	Cfg    *config.Config
	SC     config.ServerConfig
	OnNotify func(method string) // stdio upstreams only; HTTP has no server-push channel yet
}

// Dial connects to an upstream MCP server, injecting auth headers.
func Dial(ctx context.Context, p DialParams) (transport.Connection, error) {
	switch p.SC.Transport {
	case "http", "sse", "streamable":
		return transport.NewHTTPConnection(transport.HTTPConnectionConfig{
			URL:                     p.SC.URL,
			Headers:                 MergedHeaders(p.SC),
			ClientTimeout:           parseClientTimeout(p.SC.HTTPClientTimeout),
			DisableRetryOnRateLimit: p.SC.DisableRetryOnRateLimit,
			BlockPrivateIPs:         p.SC.RuntimeAdded && !p.Cfg.DangerousAllowPrivateURLs,
		})
	default:
		return transport.NewStdioConnection(ctx, transport.StdioCommand{
			Command: p.SC.Command, Args: p.SC.Args, Env: p.SC.Env, Logger: p.Logger, OnNotify: p.OnNotify,
		})
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
