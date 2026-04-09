package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func runCall(configDir string, args []string) {
	runCallCmd(configDir, args, false)
}

func runPermCall(configDir string, args []string) {
	runCallCmd(configDir, args, true)
}

type callOutput int

const (
	callOutputJSON callOutput = iota
	callOutputMini
	callOutputRaw
)

type callFlags struct {
	json bool
	mini bool
	raw  bool
}

type callContext struct {
	cfg        *config.Config
	sc         *config.ServerConfig
	serverName string
	toolName   string
	params     map[string]any
}

func runCallCmd(configDir string, args []string, protected bool) {
	f, cc := parseCallContext(configDir, args)
	checkCallPermission(cc.sc, cc.toolName, protected)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	conn := mustDialCall(ctx, configDir, cc)
	defer conn.Close()

	mode := resolveCallOutput(f, cc.cfg.ResponseFormat)
	if mode == callOutputRaw {
		executeRaw(ctx, conn, cc)
		return
	}
	executeProjected(ctx, conn, cc, mode)
}

func parseCallContext(configDir string, args []string) (callFlags, callContext) {
	f, pos := parseCallFlags(args)
	serverName, toolName, params := resolveCallPos(pos)
	cfg, sc := loadCallCtx(configDir, serverName)
	return f, callContext{cfg: cfg, sc: sc, serverName: serverName, toolName: toolName, params: params}
}

func parseCallFlags(args []string) (callFlags, []string) {
	fs := flag.NewFlagSet("call", flag.ExitOnError)
	f := callFlags{}
	fs.BoolVar(&f.json, "j", false, "JSON output (projected envelope, default)")
	fs.BoolVar(&f.mini, "m", false, "mini format (compact key:value)")
	fs.BoolVar(&f.raw, "r", false, "raw upstream response, no projection")
	fs.Parse(args) //nolint:errcheck
	return f, fs.Args()
}

func resolveCallPos(pos []string) (serverName, toolName string, params map[string]any) {
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mini call [-j|-m|-r] <server> <tool> [<json-params>|-]")
		os.Exit(2)
	}
	params, ok := parseParams(pos)
	if !ok {
		os.Exit(2)
	}
	return pos[0], pos[1], params
}

func loadCallCtx(configDir, serverName string) (*config.Config, *config.ServerConfig) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: load config: %v\n", err)
		os.Exit(2)
	}
	sc := findServerConfig(servers, serverName)
	if sc == nil {
		fmt.Fprintf(os.Stderr, "mini: server %q not found\n", serverName)
		os.Exit(2)
	}
	return cfg, sc
}

func checkCallPermission(sc *config.ServerConfig, toolName string, protected bool) {
	if protected || sc.Permissions == nil {
		return
	}
	checkExplicitPermissions(sc.Permissions, toolName)
	checkDefaultPermission(sc.Permissions.Default, toolName)
}

func checkExplicitPermissions(perm *config.PermissionsConfig, toolName string) {
	for _, p := range perm.Protected {
		if p == toolName {
			fmt.Fprintf(os.Stderr, "mini: %s is a protected tool; use perm-call\n", toolName)
			os.Exit(2)
		}
	}
	for _, h := range perm.Hidden {
		if h == toolName {
			fmt.Fprintf(os.Stderr, "mini: tool not found: %s\n", toolName)
			os.Exit(1)
		}
	}
}

func checkDefaultPermission(defaultLevel string, toolName string) {
	switch config.PermissionLevel(defaultLevel) {
	case config.PermProtected:
		fmt.Fprintf(os.Stderr, "mini: %s is a protected tool; use perm-call\n", toolName)
		os.Exit(2)
	case config.PermHidden:
		fmt.Fprintf(os.Stderr, "mini: tool not found: %s\n", toolName)
		os.Exit(1)
	}
}

func mustDialCall(ctx context.Context, configDir string, cc callContext) transport.Connection {
	if cc.sc.Auth != nil && cc.sc.Auth.Type == "oauth2" {
		injectToken(ctx, configDir, cc.sc)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	conn, err := invoke.Dial(ctx, logger, cc.cfg, *cc.sc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: connect to %s: %v\n", cc.serverName, err)
		os.Exit(2)
	}
	return conn
}

func executeRaw(ctx context.Context, conn transport.Connection, cc callContext) {
	raw, _, err := invoke.InvokeRaw(ctx, conn, cc.toolName, cc.params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(raw))
}

func executeProjected(ctx context.Context, conn transport.Connection, cc callContext, mode callOutput) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := mustCallStore(cc.cfg, logger)
	defer store.Close()

	result, err := invoke.Invoke(ctx, invoke.InvokeParams{
		Server:   cc.serverName,
		Tool:     cc.toolName,
		Params:   cc.params,
		Conn:     conn,
		ProjCfg:  resolveCallProjection(cc.sc, cc.toolName),
		ProjDefs: callProjDefaults(cc.cfg),
		Builder:  response.NewBuilder(store, cc.cfg.InlineThreshold),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: %v\n", err)
		os.Exit(1)
	}
	if result.Envelope.Error != "" {
		fmt.Fprintf(os.Stderr, "mini: tool error: %s\n", result.Envelope.Message)
		os.Exit(1)
	}
	printCallOutput(cc.serverName, cc.toolName, result.Envelope, mode)
}

func parseParams(pos []string) (map[string]any, bool) {
	if len(pos) < 3 {
		return nil, true
	}
	raw := []byte(pos[2])
	if pos[2] == "-" {
		var err error
		raw, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mini: read stdin: %v\n", err)
			return nil, false
		}
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		fmt.Fprintf(os.Stderr, "mini: invalid params JSON: %v\n", err)
		return nil, false
	}
	return params, true
}

func findServerConfig(servers []config.ServerConfig, name string) *config.ServerConfig {
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i]
		}
	}
	return nil
}

func resolveCallOutput(f callFlags, cfgFormat string) callOutput {
	switch {
	case f.raw:
		return callOutputRaw
	case f.mini:
		return callOutputMini
	case f.json:
		return callOutputJSON
	case cfgFormat == "mini":
		return callOutputMini
	default:
		return callOutputJSON
	}
}

func resolveCallProjection(sc *config.ServerConfig, toolName string) *config.ProjectionConfig {
	if sc.Projections == nil {
		return nil
	}
	if p := sc.Projections[toolName]; p != nil {
		return p
	}
	return sc.Projections["*"]
}

func callProjDefaults(cfg *config.Config) *projection.Defaults {
	return &projection.Defaults{
		StringLimit:        cfg.DefaultStringLimit,
		DepthLimit:         cfg.DefaultDepthLimit,
		ContentFields:      cfg.ContentFields,
		AutoStripThreshold: cfg.AutoStripThreshold,
	}
}

func mustCallStore(cfg *config.Config, logger *slog.Logger) *response.Store {
	sc := buildCallStoreConfig(cfg)
	store, err := response.NewStore(sc)
	if err != nil {
		logger.Warn("could not open response store, using temp dir", "err", err)
		sc.Dir = filepath.Join(os.TempDir(), "mini-responses")
		store, _ = response.NewStore(sc)
	}
	return store
}

func buildCallStoreConfig(cfg *config.Config) response.StoreConfig {
	dir := cfg.ResponseDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".mini", "responses")
	}
	ttl := time.Hour
	if cfg.ResponseTTL != "" {
		if d, err := time.ParseDuration(cfg.ResponseTTL); err == nil {
			ttl = d
		}
	}
	return response.StoreConfig{Dir: dir, TTL: ttl, BudgetMB: cfg.ResponseDiskBudgetMB, CleanupInterval: time.Hour}
}

func printCallOutput(serverName, toolName string, env *response.Envelope, mode callOutput) {
	if mode == callOutputMini {
		fmt.Print(server.RenderLines(serverName, toolName, env))
		return
	}
	b, _ := json.MarshalIndent(env, "", "  ")
	fmt.Println(string(b))
}
