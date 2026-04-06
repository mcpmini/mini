package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

var usageText = `usage: mini [--config DIR] [--version] <command>

commands:
  serve [flags]        Start the MCP proxy (default, stdio)
  daemon               Run as a shared background daemon (HTTP)
  daemon status        Show whether the daemon is running
  ls / list            List configured servers
  add NAME [flags]     Add a server
  rm / remove NAME     Remove a server
  status               Show server health
  cleanup              Delete expired response files
  auth NAME            Authorize a server via OAuth2 (PKCE flow)
  test [--timeout T]   CI-safe health check (exits 1 on any failure)
  init / setup [--yes] Interactive setup wizard
  version              Print version

serve flags:
  --http ADDR         Also serve HTTP MCP on ADDR; bare port or :port binds to loopback
  --standalone        Skip daemon detection, serve directly
  --dangerous-nonloopback-http  Allow --http to bind to non-loopback (all clients must be trusted)

add flags:
  --url URL           HTTP/SSE server URL
  --cmd CMD [ARGS]    Stdio command (default if no --url)
  --header K=V        HTTP header (repeatable)
  --protected TOOL    Mark tool as protected (repeatable)
  --from-claude PATH  Import from Claude Desktop / Claude Code config JSON
  --from-cursor PATH  Import from Cursor mcp.json
  --from-codex PATH   Import from Codex config.toml
  --from-gemini PATH  Import from Gemini CLI settings.json`

func main() {
	fs := flag.NewFlagSet("mini", flag.ContinueOnError)
	configDir := fs.String("config", config.DefaultConfigDir(), "config directory")
	versionFlag := fs.Bool("version", false, "print version and exit")
	fs.Usage = usage
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *versionFlag {
		fmt.Println(transport.Version)
		return
	}
	dispatch(*configDir, fs.Args())
}

var commands = map[string]func(string, []string){
	"serve":  runServe,
	"daemon": runDaemonCmd,
	"ls":     func(dir string, _ []string) { mustRun(runList(dir, os.Stdout)) },
	"list":   func(dir string, _ []string) { mustRun(runList(dir, os.Stdout)) },
	"add":    func(dir string, args []string) { mustRun(runAdd(dir, args, os.Stdout)) },
	"rm":     func(dir string, args []string) { mustRun(runRemove(dir, args, os.Stdout)) },
	"remove": func(dir string, args []string) { mustRun(runRemove(dir, args, os.Stdout)) },
	"status": func(dir string, _ []string) { runStatus(dir) },
	"cleanup": func(dir string, _ []string) { mustRun(runCleanup(dir, os.Stdout)) },
	"auth":    runAuth,
	"test":    runTest,
	"init":    runInit,
	"setup":   runInit,
	"version": func(_ string, _ []string) { fmt.Println(transport.Version) },
}

func mustRun(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func dispatch(configDir string, args []string) {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	run, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(2)
	}
	run(configDir, args)
}

func runDaemonCmd(configDir string, args []string) {
	if len(args) > 0 && args[0] == "status" {
		runDaemonStatus(configDir)
	} else {
		runDaemon(configDir, args)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, usageText)
}

func runServe(configDir string, args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	logLevel := fs.String("log-level", "", "log level (debug|info|warn|error)")
	httpAddr := fs.String("http", "", "also listen for HTTP MCP connections on this address (e.g. :4857)")
	standalone := fs.Bool("standalone", false, "skip daemon detection, serve directly (useful for debugging)")
	dangerNonLoopback := fs.Bool("dangerous-nonloopback-http", false, "allow --http to bind to a non-loopback address (only when all network clients are trusted)")
	fs.Parse(args) //nolint:errcheck

	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	logger := buildLogger(cfg, *logLevel)
	if !*standalone && *httpAddr == "" {
		if err := tryProxyMode(configDir, logger); err == nil {
			return
		}
	}
	serveStandalone(configDir, cfg, servers, logger, *httpAddr, *dangerNonLoopback)
}

func serveStandalone(configDir string, cfg *config.Config, servers []config.ServerConfig, logger *slog.Logger, httpAddr string, dangerNonLoopback bool) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	injectOAuthTokens(ctx, configDir, servers)
	srv := buildAndConnectServer(ctx, cfg, configDir, logger, servers)
	defer srv.Close()
	httpSrv := maybeStartHTTP(httpAddr, srv, logger, dangerNonLoopback)
	maybeStartSessionEviction(ctx, httpSrv, srv)
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

func buildAndConnectServer(ctx context.Context, cfg *config.Config, configDir string, logger *slog.Logger, servers []config.ServerConfig) *server.Server {
	srv := server.NewWithConfigDir(cfg, configDir, logger)
	for _, sc := range servers {
		if isEnabled(sc) {
			if err := srv.AddUpstream(ctx, sc); err != nil {
				logger.Error("failed to connect upstream", "server", sc.Name, "err", err)
				os.Exit(1)
			}
		}
	}
	if err := srv.LoadActions(configDir); err != nil {
		logger.Warn("failed to load actions", "err", err)
	}
	return srv
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

// tryProxyMode tries to connect to (or start) the daemon and proxy stdio to it.
// Returns nil on clean proxy exit, non-nil if daemon is unavailable.
func tryProxyMode(configDir string, logger *slog.Logger) error {
	port := daemon.RunningPort(configDir)
	if port == 0 {
		var err error
		port, err = daemon.Start(configDir, 3*time.Second)
		if err != nil {
			logger.Warn("daemon unavailable, running standalone", "err", err)
			return err
		}
	}
	sessionID := transport.NewSessionID()
	logger.Info("connected to mini daemon", "port", port, "session", sessionID)
	return proxy.Run(port, sessionID, os.Stdin, os.Stdout)
}

func buildLogger(cfg *config.Config, override string) *slog.Logger {
	level := resolveLogLevel(cfg, override)
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
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

func isEnabled(sc config.ServerConfig) bool {
	return sc.Enabled == nil || *sc.Enabled
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

func checkLoopbackPolicy(addr, resolved string, nonLoopback, dangerNonLoopback bool, logger *slog.Logger) {
	if nonLoopback && !dangerNonLoopback {
		fatalf("--http %q binds to a non-loopback address; pass --dangerous-nonloopback-http to allow this (ensures all network clients are trusted)", addr)
	}
	if nonLoopback {
		logger.Warn("HTTP server binding to non-loopback address; ensure all network clients are trusted", "addr", resolved)
	}
}

func startHTTPServer(addr string, handler http.Handler, logger *slog.Logger, dangerNonLoopback bool) *http.Server {
	resolved, nonLoopback := resolveHTTPAddr(addr)
	checkLoopbackPolicy(addr, resolved, nonLoopback, dangerNonLoopback, logger)
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
