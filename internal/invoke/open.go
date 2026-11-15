package invoke

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

const defaultHandshakeTimeout = 30 * time.Second

type OpenParams struct {
	DialParams
	// Nil uses the system clock, independently of Clock.
	HandshakeClock clock.Clock
}

// Open dials the server and lists its tools within its handshake_timeout. A listing refused for a
// missing OAuth login records the requirement and returns an error wrapping
// transport.ErrReauthRequired.
func Open(ctx context.Context, op OpenParams) (transport.Connection, transport.ToolList, error) {
	p := op.withDefaults()
	openCtx, cancel, timeout := withHandshakeTimeout(ctx, op.HandshakeClock, p.Server.HandshakeTimeout)
	defer cancel()
	// Names the setting to raise; clock.WithTimeout also expires as Canceled, which would read as
	// if the caller gave up.
	explainTimeout := func(err error) error {
		if ctx.Err() == nil && openCtx.Err() != nil {
			return fmt.Errorf("no reply within handshake_timeout (%s): %w", timeout, context.DeadlineExceeded)
		}
		return err
	}
	conn, err := Dial(openCtx, p)
	if err != nil {
		return nil, transport.ToolList{}, fmt.Errorf("connect to %s: %w", p.Server.Name, explainTimeout(err))
	}
	tools, err := conn.ListTools(openCtx)
	if err != nil {
		conn.Close() //nolint:errcheck // The listing error is what the caller needs.
		err = fmt.Errorf("list tools from %s: %w", p.Server.Name, explainTimeout(err))
		return nil, transport.ToolList{}, markOAuthIfRequired(ctx, p, err)
	}
	return conn, tools, nil
}

func (p DialParams) withDefaults() DialParams {
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return p
}

func withHandshakeTimeout(
	ctx context.Context,
	c clock.Clock,
	spec string,
) (context.Context, context.CancelFunc, time.Duration) {
	d, bounded := handshakeTimeout(spec)
	if !bounded {
		return ctx, func() {}, 0
	}
	if c == nil {
		// Stdio's legacy fallback needs a real deadline to reserve restart time.
		deadlineCtx, cancel := context.WithTimeout(ctx, d)
		return deadlineCtx, cancel, d
	}
	ctx, cancel := clock.WithTimeout(ctx, c, d)
	return ctx, cancel, d
}

func handshakeTimeout(spec string) (time.Duration, bool) {
	ts, err := config.ParseTimeoutSpec(spec, defaultHandshakeTimeout)
	if err != nil {
		slog.Warn("invalid handshake_timeout spec, using default", "spec", spec, "default", defaultHandshakeTimeout)
		return defaultHandshakeTimeout, true
	}
	return ts.Duration, ts.Enabled
}

func markOAuthIfRequired(ctx context.Context, p DialParams, connErr error) error {
	required, err := ops.DetectOAuth(
		ctx,
		ops.DetectOAuthParams{ConfigDir: p.ConfigDir, Server: p.Server, ConnErr: connErr},
	)
	if err != nil {
		p.Logger.Warn("persist discovered oauth requirement", "server", p.Server.Name, "err", err)
	}
	if !required {
		return connErr
	}
	return fmt.Errorf(
		"%s requires OAuth authorization; run `mini auth %s`: %w: %w",
		p.Server.Name, p.Server.Name, transport.ErrReauthRequired, connErr,
	)
}

type ProbeParams struct {
	Config    *config.Config
	ConfigDir string
	Server    config.ServerConfig
}

// Probe opens the server as the daemon would, then closes it. It returns the number of tools an
// agent would see, so hidden and duplicate tools don't count.
func Probe(ctx context.Context, p ProbeParams) (int, error) {
	providers := provider.NewRegistry()
	defer providers.Close()
	conn, tools, err := Open(ctx, OpenParams{DialParams: DialParams{
		Config: p.Config, Server: p.Server, ConfigDir: p.ConfigDir, ProviderRegistry: providers,
	}})
	if err != nil {
		return 0, err
	}
	conn.Close() //nolint:errcheck // Cleanup can't change the probe's answer.
	return visibleToolCount(p.Server, tools), nil
}

func visibleToolCount(sc config.ServerConfig, tools transport.ToolList) int {
	reg := registry.New()
	reg.AddServer(registry.ServerParams{
		Name: sc.Name, Defs: tools.Tools, Perm: sc.Permissions,
		AliasByToolName: config.AliasesFromProjections(sc.Projections),
	})
	return reg.ToolCount(sc.Name)
}

// ProbeServer is Probe with the main config loaded from configDir; it reports only whether the
// server connects.
func ProbeServer(ctx context.Context, configDir string, sc config.ServerConfig) error {
	cfg, err := config.LoadMain(configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	_, err = Probe(ctx, ProbeParams{Config: cfg, ConfigDir: configDir, Server: sc})
	return err
}
