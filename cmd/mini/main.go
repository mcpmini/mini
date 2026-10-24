package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/proxy"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type connectFlags struct {
	logLevel   string
	standalone bool
}

func newConnectCmd(opts *rootOptions) *cobra.Command {
	f := connectFlags{}
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect an agent to mini (stdio MCP)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnect(opts.configDir, f)
		},
	}
	bindConnectFlags(cmd, &f)
	return cmd
}

func runConnect(configDir string, f connectFlags) error {
	cfg, servers, err := loadConfig(configDir)
	if err != nil {
		fatalf("%v", err)
	}
	logger := buildLogger(cfg, f.logLevel, os.Stderr)
	if !f.standalone && connectViaDaemon(configDir, logger) == nil {
		return nil
	}
	return serveStandalone(ServeParams{
		ConfigDir: configDir, Cfg: cfg, Servers: servers.Loaded, Logger: logger,
	})
}

type ServeParams struct {
	ConfigDir string
	Cfg       *config.Config
	Servers   []config.ServerConfig
	Logger    *slog.Logger
}

type serveFunc func(context.Context, io.Reader, io.Writer) error

type serveWatchParams struct {
	Ctx   context.Context
	Serve serveFunc
	In    io.ReadCloser
	Out   io.Writer
}

func serveUntilCanceled(p serveWatchParams) error {
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	output := newServeOutput(p.Out, cancel, p.In)
	defer func() {
		_ = p.In.Close() //nolint:errcheck // Owned input cleanup is outside the Serve result contract.
	}()
	done := make(chan error, 1)
	go func() { done <- p.Serve(ctx, p.In, output) }()
	select {
	case err := <-done:
		return output.result(err)
	case <-p.Ctx.Done():
		_ = p.In.Close() //nolint:errcheck // Input-close errors do not turn requested cancellation into a serve failure.
		<-done
		return output.result(nil)
	}
}

// Blocking stdin isn't in Go's runtime poller, so closing os.Stdin can't interrupt a pending Read; a pipe reader can.
func stdinPipe(src io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		_, err := io.Copy(pw, src)
		pw.CloseWithError(err)
	}()
	return pr
}

type notifyFunc func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)

func shutdownContext(notify notifyFunc) (context.Context, context.CancelFunc) {
	ctx, stop := notify(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// A second signal must still kill the process if the graceful drain hangs.
	context.AfterFunc(ctx, stop)
	return ctx, stop
}

func serveStandalone(p ServeParams) error {
	ctx, stop := shutdownContext(signal.NotifyContext)
	defer stop()
	srv := buildAndStart(ctx, BuildServerParams{
		Cfg: p.Cfg, ConfigDir: p.ConfigDir, Logger: p.Logger, Servers: p.Servers,
	})
	defer srv.Close()
	p.Logger.Info("mini ready")
	err := serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: srv.Serve, In: stdinPipe(os.Stdin), Out: os.Stdout})
	srv.ReleaseStartupHolds()
	if err != nil {
		return fmt.Errorf("serve stdio: %w", err)
	}
	return nil
}

type BuildServerParams struct {
	Cfg             *config.Config
	ConfigDir       string
	Logger          *slog.Logger
	Servers         []config.ServerConfig
	Clock           clock.Clock
	DaemonAuthToken string
}

func buildAndStart(ctx context.Context, p BuildServerParams) *server.Server {
	srv := server.New(server.Params{
		Config: p.Cfg, ConfigDir: p.ConfigDir, Logger: p.Logger,
		Clock:           p.Clock,
		DaemonAuthToken: p.DaemonAuthToken,
	})
	srv.ConnectUpstreams(ctx, p.Servers)
	srv.StartConfigReload(ctx)
	return srv
}

func connectViaDaemon(configDir string, logger *slog.Logger) error {
	if err := daemon.CheckSocketPath(configDir); err != nil {
		return err
	}
	reresolve := daemonReresolver(configDir, logger)
	token, err := reresolve()
	if err != nil {
		return err
	}
	socket := daemon.SocketPath(configDir)
	sessionID := transport.NewSessionID()
	logger.Info("connected to mini daemon", "socket", socket, "session", sessionID)
	return proxy.Run(proxy.RunParams{
		Client: daemon.SocketClient(socket, 0), SessionID: sessionID, Token: token,
		In: os.Stdin, Out: os.Stdout, Resolver: proxy.NewDaemonResolver(reresolve),
		Clock: clock.System(),
	})
}

func daemonReresolver(configDir string, logger *slog.Logger) func() (string, error) {
	return func() (string, error) {
		if err := ensureDaemonRunning(configDir, logger); err != nil {
			return "", err
		}
		token, err := daemon.ReadToken(configDir)
		if err != nil {
			return "", fmt.Errorf("read daemon token: %w", err)
		}
		return token, nil
	}
}

func ensureDaemonRunning(configDir string, logger *slog.Logger) error {
	if daemon.Running(configDir) {
		return nil
	}
	if err := daemon.Start(configDir, 3*time.Second, clock.System()); err != nil {
		logger.Warn("daemon unavailable", "err", err)
		return err
	}
	return nil
}

func buildLogger(cfg *config.Config, override string, w io.Writer) *slog.Logger {
	level := resolveLogLevel(cfg, override)
	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger
}

func resolveLogLevel(cfg *config.Config, override string) slog.Level {
	raw := cfg.LogLevel
	if override != "" {
		raw = override
	}
	var level slog.Level
	if raw != "" {
		if err := level.UnmarshalText([]byte(raw)); err != nil {
			fmt.Fprintf(os.Stderr, "warning: invalid log level %q, using INFO\n", raw)
		}
	}
	return level
}

func printNotice(out io.Writer, format string, args ...any) {
	//nolint:errcheck // Notice delivery does not change saved configuration or returned operation errors.
	_, _ = fmt.Fprintf(out, format, args...)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mini: "+format+"\n", args...)
	os.Exit(1)
}

func bindConnectFlags(cmd *cobra.Command, f *connectFlags) {
	fl := cmd.Flags()
	fl.StringVar(&f.logLevel, "log-level", "", "log level (debug|info|warn|error)")
	fl.BoolVar(&f.standalone, "standalone", false, "skip daemon detection, serve directly (useful for debugging)")
}
