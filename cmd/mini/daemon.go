package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/server"
)

func runDaemon(configDir string, args []string) {
	port, logLevel := parseDaemonFlags(args)
	cfg, servers := loadDaemonConfig(configDir)
	portFile := ensureDaemonNotRunning(configDir)
	logW := daemon.OpenCappedLog(filepath.Join(configDir, "daemon.log"))
	defer logW.Close()
	logger := buildLogger(cfg, logLevel, logW)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Bind before minting: a daemon that loses the port race dies here without
	// touching the shared token file, so it can't leave a stale token behind.
	ln, actualPort := bindPort(resolveDaemonListenPort(cfg, port))
	serveDaemon(ctx, DaemonServeParams{
		ConfigDir: configDir, Cfg: cfg, Servers: servers, Logger: logger,
		Listener: ln, Port: actualPort, PortFile: portFile,
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
	Port      int
	PortFile  string
}

func serveDaemon(ctx context.Context, p DaemonServeParams) {
	injectOAuthTokens(ctx, p.ConfigDir, p.Servers)
	token := mintDaemonToken(p.ConfigDir)
	// Best-effort cleanup on exit; a stale token is inert and the next daemon overwrites it.
	defer func() { _ = os.Remove(daemon.TokenFile(p.ConfigDir)) }()
	srv := buildAndConnectServer(ctx, BuildServerParams{Cfg: p.Cfg, ConfigDir: p.ConfigDir, Logger: p.Logger, Servers: p.Servers}, server.WithDaemonAuthToken(token))
	defer srv.Close()
	startDaemonHTTP(ctx, DaemonHTTPParams{Srv: srv, PortFile: p.PortFile, Listener: p.Listener, Port: p.Port})
}

func resolveDaemonListenPort(cfg *config.Config, flagPort int) int {
	if flagPort >= 0 {
		return flagPort
	}
	return cfg.DaemonPort
}

func mintDaemonToken(configDir string) string {
	token, err := daemon.WriteToken(configDir)
	if err != nil {
		fatalf("write daemon token: %v", err)
	}
	return token
}

func ensureDaemonNotRunning(configDir string) string {
	portFile := daemon.PortFile(configDir)
	if daemon.RunningPort(configDir) != 0 {
		fatalf("daemon already running (port file: %s)", portFile)
	}
	return portFile
}

func parseDaemonFlags(args []string) (int, string) {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	port := fs.Int("port", -1, "port to listen on (-1 = use daemon_port from config; 0 = OS-assigned random port)")
	logLevel := fs.String("log-level", "", "log level (debug|info|warn|error)")
	fs.Parse(args) //nolint:errcheck
	return *port, *logLevel
}

type DaemonHTTPParams struct {
	Srv      *server.Server
	PortFile string
	Listener net.Listener
	Port     int
}

func startDaemonHTTP(ctx context.Context, p DaemonHTTPParams) {
	writePortFile(p.PortFile, p.Port)
	defer os.Remove(p.PortFile)
	httpSrv := daemonHTTPServer(p.Srv)
	go httpSrv.Serve(p.Listener) //nolint:errcheck
	go p.Srv.RunSessionEviction(ctx, 30*time.Minute)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdownCtx) //nolint:errcheck
}

func bindPort(port int) (net.Listener, int) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fatalf("listen: %v", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		fatalf("listener address is not TCP: %T", ln.Addr())
	}
	return ln, tcpAddr.Port
}

func writePortFile(portFile string, port int) {
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)), 0600); err != nil {
		fatalf("write port file: %v", err)
	}
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
	portFile := daemon.PortFile(configDir)
	portNum, err := readDaemonPort(portFile)
	if err != nil {
		printDaemonStatusReadErr(err)
		return
	}
	fetchDaemonHealth(portNum)
}

func fetchDaemonHealth(portNum int) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", portNum))
	if err != nil {
		fmt.Printf("daemon: port file exists (port %d) but not responding\n", portNum)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("daemon: running on port %d — %s\n", portNum, body)
}

func readDaemonPort(portFile string) (int, error) {
	data, err := os.ReadFile(portFile)
	if err != nil {
		return 0, err
	}
	portNum, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || portNum < 1 || portNum > 65535 {
		return 0, fmt.Errorf("port file %s contains invalid port", portFile)
	}
	return portNum, nil
}

func printDaemonStatusReadErr(err error) {
	if os.IsNotExist(err) {
		fmt.Println("daemon: not running")
		return
	}
	fmt.Printf("daemon: %v\n", err)
}
