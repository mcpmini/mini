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
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/server"
)

func newDaemonCmd(opts *rootOptions) *cobra.Command {
	var logLevel string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run as a shared background daemon (HTTP)",
		RunE: func(cmd *cobra.Command, args []string) error {
			runDaemon(opts.configDir, logLevel)
			return nil
		},
	}
	cmd.Flags().StringVar(&logLevel, "log-level", "", "log level (debug|info|warn|error)")
	cmd.AddCommand(newDaemonStatusCmd(opts))
	return cmd
}

func newDaemonStatusCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the daemon is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			runDaemonStatus(opts.configDir)
			return nil
		},
	}
}

func runDaemon(configDir string, logLevel string) {
	if err := daemon.CheckSocketPath(configDir); err != nil {
		fatalf("%v", err)
	}
	cfg, servers := loadDaemonConfig(configDir)
	socket := ensureDaemonNotRunning(configDir)
	logW := daemon.OpenCappedLog(filepath.Join(configDir, "internal", "daemon", "daemon.log"))
	defer logW.Close()
	logger := buildLogger(cfg, logLevel, logW)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ln := bindSocket(socket)
	serveDaemon(ctx, DaemonServeParams{
		ConfigDir: configDir, Cfg: cfg, Servers: servers, Logger: logger, Listener: ln,
	})
}

func loadDaemonConfig(configDir string) (*config.Config, []config.ServerConfig) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	return cfg, servers
}

type DaemonServeParams struct {
	ConfigDir string
	Cfg       *config.Config
	Servers   []config.ServerConfig
	Logger    *slog.Logger
	Listener  net.Listener
}

func serveDaemon(ctx context.Context, p DaemonServeParams) {
	injectOAuthTokens(ctx, p.ConfigDir, p.Servers)
	token := mintDaemonToken(p.ConfigDir)
	srv := buildAndStartConnecting(ctx, BuildServerParams{Cfg: p.Cfg, ConfigDir: p.ConfigDir, Logger: p.Logger, Servers: p.Servers}, server.WithDaemonAuthToken(token))
	defer srv.Close()
	startDaemonHTTP(ctx, DaemonHTTPParams{Srv: srv, Listener: p.Listener})
}

func mintDaemonToken(configDir string) string {
	token, err := daemon.EnsureToken(configDir)
	if err != nil {
		fatalf("write daemon token: %v", err)
	}
	return token
}

func ensureDaemonNotRunning(configDir string) string {
	if daemon.Running(configDir) {
		fatalf("daemon already running (socket: %s)", daemon.SocketPath(configDir))
	}
	return daemon.SocketPath(configDir)
}

type DaemonHTTPParams struct {
	Srv      *server.Server
	Listener net.Listener
}

func startDaemonHTTP(ctx context.Context, p DaemonHTTPParams) {
	httpSrv := daemonHTTPServer(p.Srv)
	go httpSrv.Serve(p.Listener) //nolint:errcheck
	go p.Srv.RunSessionEviction(ctx, 30*time.Minute)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Closing the listener unlinks the socket; a SIGKILL leaves a stale one for the next bindSocket to reclaim.
	httpSrv.Shutdown(shutdownCtx) //nolint:errcheck
}

func bindSocket(socket string) net.Listener {
	dir := filepath.Dir(socket)
	if err := os.MkdirAll(dir, 0700); err != nil {
		fatalf("create socket dir: %v", err)
	}
	// The dir's permissions are the access boundary — macOS ignores the socket file's own mode on connect.
	_ = os.Chmod(dir, 0700)
	ln, err := net.Listen("unix", socket)
	// Binding the socket is the single-winner election: if another daemon is healthy
	// on this socket we exit; if a stale socket remains from a SIGKILL we reclaim it.
	if err != nil {
		if daemon.SocketHealthy(socket) {
			os.Exit(0)
		}
		_ = os.Remove(socket)
		if ln, err = net.Listen("unix", socket); err != nil {
			fatalf("bind socket %s: %v", socket, err)
		}
	}
	// Linux honors the socket file's own mode on connect; a permissive umask would otherwise leave it world-writable.
	_ = os.Chmod(socket, 0600)
	return ln
}

func daemonHTTPServer(srv *server.Server) *http.Server {
	return &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: per-call deadlines are enforced by ToolTimeout.
		// A fixed WriteTimeout would silently truncate any tool configured
		// with tool_timeout longer than the cap.
		MaxHeaderBytes: 64 << 10,
	}
}

func runDaemonStatus(configDir string) {
	resp, err := daemon.SocketClient(daemon.SocketPath(configDir), 2*time.Second).Get("http://localhost/healthz")
	if err != nil {
		fmt.Println("daemon: not running")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("daemon: unhealthy (HTTP %d) — %s\n", resp.StatusCode, body)
		return
	}
	fmt.Printf("daemon: running — %s\n", body)
}
