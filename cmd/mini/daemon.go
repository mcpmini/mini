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
	ln, err := bindSocket(socketBindParams{Socket: socket, Chmod: os.Chmod})
	if err != nil {
		fatalf("%v", err)
	}
	if ln == nil {
		return
	}
	serveDaemon(ctx, DaemonServeParams{
		ConfigDir: configDir, Cfg: cfg, Servers: servers.Loaded, Logger: logger, Listener: ln,
	})
}

func loadDaemonConfig(configDir string) (*config.Config, config.Servers) {
	cfg, servers, err := loadConfig(configDir)
	if err != nil {
		fatalf("%v", err)
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
	token := mintDaemonToken(p.ConfigDir)
	srv := buildAndStart(ctx, BuildServerParams{
		Cfg: p.Cfg, ConfigDir: p.ConfigDir, Logger: p.Logger, Servers: p.Servers,
		DaemonAuthToken: token,
	})
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
	p.Srv.ReleaseStartupHolds()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Closing the listener unlinks the socket; a SIGKILL leaves a stale one for the next bindSocket to reclaim.
	//nolint:contextcheck // The service context is canceled; graceful shutdown needs a fresh bounded context.
	httpSrv.Shutdown(shutdownCtx) //nolint:errcheck
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
