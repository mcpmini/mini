package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

const standaloneHTTPSessionMaxIdle = 30 * time.Minute

type sessionEvictor interface {
	RunSessionEviction(context.Context, time.Duration)
}

type connectFlags struct {
	logLevel          string
	httpAddr          string
	standalone        bool
	dangerNonLoopback bool
	toolMode          transport.ToolMode
}

func newConnectCmd(opts *rootOptions) *cobra.Command {
	f := connectFlags{}
	var toolModeStr string
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect an agent to mini (stdio MCP)",
		RunE: func(cmd *cobra.Command, args []string) error {
			f.toolMode = parseToolMode(toolModeStr)
			runConnect(opts.configDir, f)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.logLevel, "log-level", "", "log level (debug|info|warn|error)")
	fl.StringVar(&f.httpAddr, "http", "", "also listen for HTTP MCP connections on this address (e.g. :4857)")
	fl.BoolVar(&f.standalone, "standalone", false, "skip daemon detection, serve directly (useful for debugging)")
	fl.BoolVar(&f.dangerNonLoopback, "dangerous-nonloopback-http", false, "allow --http to bind to a non-loopback address")
	fl.StringVar(&toolModeStr, "tool-mode", "", "tool interface: compact for the four-meta-tool interface (default is proxy)")
	return cmd
}

func parseToolMode(m string) transport.ToolMode {
	switch m {
	case transport.ToolModeCompactValue:
		return transport.ToolModeCompact
	case "", transport.ToolModeProxyValue:
		return transport.ToolModeProxy
	}
	fatalf("invalid --tool-mode %q; valid values: proxy, compact", m)
	return transport.ToolModeProxy
}

func runConnect(configDir string, f connectFlags) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	logger := buildLogger(cfg, f.logLevel, os.Stderr)
	if shouldTryDaemon(f.standalone, f.httpAddr) && connectViaDaemon(configDir, logger, f.toolMode) == nil {
		return
	}
	var opts []server.ServerOption
	if f.toolMode == transport.ToolModeCompact {
		opts = []server.ServerOption{server.WithToolMode(transport.ToolModeCompact)}
	}
	serveStandalone(ServeParams{ConfigDir: configDir, Cfg: cfg, Servers: servers, Logger: logger, HTTPAddr: f.httpAddr, DangerNonLoopback: f.dangerNonLoopback}, opts...)
}

func shouldTryDaemon(standalone bool, httpAddr string) bool {
	return !standalone && httpAddr == ""
}

type ServeParams struct {
	ConfigDir         string
	Cfg               *config.Config
	Servers           []config.ServerConfig
	Logger            *slog.Logger
	HTTPAddr          string
	DangerNonLoopback bool
}

func serveStandalone(p ServeParams, opts ...server.ServerOption) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	injectOAuthTokens(ctx, p.ConfigDir, p.Servers)
	opts = appendNonLoopbackHostOpt(opts, p.HTTPAddr)
	srv := buildAndStartConnecting(ctx, BuildServerParams{Cfg: p.Cfg, ConfigDir: p.ConfigDir, Logger: p.Logger, Servers: p.Servers}, opts...)
	defer srv.Close()
	httpSrv := maybeStartHTTP(p.HTTPAddr, srv, p.Logger, p.DangerNonLoopback)
	maybeStartSessionEviction(ctx, httpSrv, srv)
	logger := p.Logger
	logger.Info("mini ready")
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		logger.Error("serve error", "err", err)
		os.Exit(1)
	}
	shutdownHTTP(httpSrv)
}

func shutdownHTTP(httpSrv *http.Server) {
	if httpSrv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx) //nolint:errcheck
}

type BuildServerParams struct {
	Cfg       *config.Config
	ConfigDir string
	Logger    *slog.Logger
	Servers   []config.ServerConfig
}

// buildAndStartConnecting builds the server and kicks off upstream connects in the
// background; it returns before any upstream resolves so Serve can start answering
// the agent's initialize immediately (#33). Late-arriving upstreams announce
// themselves via the existing tools/list_changed notification.
func buildAndStartConnecting(ctx context.Context, p BuildServerParams, opts ...server.ServerOption) *server.Server {
	srv := server.NewWithConfigDir(p.Cfg, p.ConfigDir, p.Logger, opts...)
	srv.ConnectUpstreams(ctx, p.Servers)
	return srv
}

// Without this, the DNS-rebinding Host check rejects legitimate remote clients.
func appendNonLoopbackHostOpt(opts []server.ServerOption, httpAddr string) []server.ServerOption {
	if httpAddr == "" {
		return opts
	}
	if _, nonLoopback := resolveHTTPAddr(httpAddr); nonLoopback {
		return append(opts, server.WithAllowNonLoopbackHost())
	}
	return opts
}

func maybeStartHTTP(addr string, handler http.Handler, logger *slog.Logger, dangerNonLoopback bool) *http.Server {
	if addr == "" {
		return nil
	}
	return startHTTPServer(addr, handler, logger, dangerNonLoopback)
}

func maybeStartSessionEviction(ctx context.Context, httpSrv *http.Server, srv sessionEvictor) {
	if httpSrv == nil {
		return
	}
	go srv.RunSessionEviction(ctx, standaloneHTTPSessionMaxIdle)
}

func connectViaDaemon(configDir string, logger *slog.Logger, mode transport.ToolMode) error {
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
		In: os.Stdin, Out: os.Stdout, ToolMode: mode, Resolver: proxy.NewDaemonResolver(reresolve),
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

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mini: "+format+"\n", args...)
	os.Exit(1)
}

// resolveHTTPAddr canonicalizes the --http flag value.
// A bare port ("4857") or ":port" binds to loopback (127.0.0.1).
// Any other address is accepted but considered non-loopback.
func resolveHTTPAddr(addr string) (resolved string, nonLoopback bool) {
	if !strings.Contains(addr, ":") {
		return "127.0.0.1:" + addr, false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1:" + addr, false
	}
	if host == "" {
		return "127.0.0.1:" + port, false
	}
	ip := net.ParseIP(host)
	return addr, ip == nil || !ip.IsLoopback()
}

type loopbackPolicyParams struct {
	Addr              string
	Resolved          string
	NonLoopback       bool
	DangerNonLoopback bool
	Logger            *slog.Logger
}

func checkLoopbackPolicy(p loopbackPolicyParams) {
	if p.NonLoopback && !p.DangerNonLoopback {
		fatalf("--http %q binds to a non-loopback address; pass --dangerous-nonloopback-http to allow this (ensures all network clients are trusted)", p.Addr)
	}
	if p.NonLoopback {
		p.Logger.Warn("HTTP server binding to non-loopback address; ensure all network clients are trusted", "addr", p.Resolved)
	}
}

func startHTTPServer(addr string, handler http.Handler, logger *slog.Logger, dangerNonLoopback bool) *http.Server {
	resolved, nonLoopback := resolveHTTPAddr(addr)
	checkLoopbackPolicy(loopbackPolicyParams{Addr: addr, Resolved: resolved, NonLoopback: nonLoopback, DangerNonLoopback: dangerNonLoopback, Logger: logger})
	ln, err := net.Listen("tcp", resolved)
	if err != nil {
		fatalf("listen: %v", err)
	}
	logger.Info("mini HTTP listening", "addr", resolved)
	httpSrv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 64 << 10}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "err", err)
		}
	}()
	return httpSrv
}
