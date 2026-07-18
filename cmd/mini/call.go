package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newCallCmd(opts *rootOptions) *cobra.Command {
	return newCallCommand(opts, false)
}

func newCallCommand(opts *rootOptions, protected bool) *cobra.Command {
	f := callFlags{}
	cmd := &cobra.Command{
		Use:   "call SERVER TOOL [PARAMS]",
		Short: "Invoke an open tool directly (exit 1 on tool error)",
		Args:  usageArgs(cobra.RangeArgs(2, 3)),
		PreRunE: func(*cobra.Command, []string) error {
			if f.enabledCount() > 1 {
				return usageErrf("choose only one output mode: --json, --toon, or --raw")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			runCallCmd(opts.configDir, args, f, protected)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&f.json, "json", "j", false, "JSON output (projected envelope, default)")
	cmd.Flags().BoolVarP(&f.toon, "toon", "t", false, "TOON format (token-oriented object notation)")
	cmd.Flags().BoolVarP(&f.raw, "raw", "r", false, "raw upstream response, no projection")
	return cmd
}

func newPermCallCmd(opts *rootOptions) *cobra.Command {
	cmd := newCallCommand(opts, true)
	cmd.Use = "perm-call SERVER TOOL [PARAMS]"
	cmd.Short = "Invoke a protected tool directly"
	return cmd
}

type callOutput int

const (
	callOutputJSON callOutput = iota
	callOutputToon
	callOutputRaw
)

type callFlags struct {
	json bool
	toon bool
	raw  bool
}

func (f callFlags) enabledCount() int {
	count := 0
	for _, enabled := range []bool{f.json, f.toon, f.raw} {
		if enabled {
			count++
		}
	}
	return count
}

type callContext struct {
	cfg        *config.Config
	sc         *config.ServerConfig
	configDir  string
	serverName string
	toolName   string
	params     map[string]any
	clock      clock.Clock
}

func runCallCmd(configDir string, args []string, f callFlags, protected bool) {
	cc := parseCallContext(configDir, args)
	cc.clock = clock.System()
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

func parseCallContext(configDir string, args []string) callContext {
	params, ok := parseParams(args)
	if !ok {
		os.Exit(2)
	}
	serverName, toolName := args[0], args[1]
	cfg, sc := loadCallCtx(configDir, serverName)
	return callContext{cfg: cfg, sc: sc, configDir: configDir, serverName: serverName, toolName: toolName, params: params}
}

func loadCallCtx(configDir, serverName string) (*config.Config, *config.ServerConfig) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: load config: %v\n", err)
		os.Exit(2)
	}
	sc := config.FindServer(servers, serverName)
	if sc == nil {
		fmt.Fprintf(os.Stderr, "mini: server %q not found\n", serverName)
		os.Exit(2)
	}
	return cfg, sc
}

func checkCallPermission(sc *config.ServerConfig, toolName string, protected bool) {
	if sc.Permissions == nil {
		return
	}
	if code, msg, blocked := callPermissionError(sc.Permissions, toolName, protected); blocked {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(code)
	}
}

func callPermissionError(perm *config.PermissionsConfig, toolName string, protected bool) (int, string, bool) {
	level := perm.LevelFor(toolName)
	if level == config.PermHidden {
		return 1, fmt.Sprintf("mini: tool not found: %s", toolName), true
	}
	if protected || level != config.PermProtected {
		return 0, "", false
	}
	return 2, fmt.Sprintf("mini: %s is a protected tool; use perm-call", toolName), true
}

func mustDialCall(ctx context.Context, configDir string, cc callContext) transport.Connection {
	if cc.sc.Auth != nil && cc.sc.Auth.Type == config.AuthTypeOAuth2 {
		injectToken(ctx, configDir, cc.sc)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	conn, err := invoke.Dial(ctx, invoke.DialParams{Logger: logger, Config: cc.cfg, Server: *cc.sc, Clock: cc.clock})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: connect to %s: %v\n", cc.serverName, err)
		os.Exit(2)
	}
	return conn
}

func executeRaw(ctx context.Context, conn transport.Connection, cc callContext) {
	raw, _, err := invoke.InvokeRaw(ctx, invoke.InvokeRawParams{Clock: cc.clock, Conn: conn, Tool: cc.toolName, Params: cc.params})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(raw))
}

func executeProjected(ctx context.Context, conn transport.Connection, cc callContext, mode callOutput) {
	store := openCallStore(cc.cfg, cc.configDir, cc.clock)
	defer store.Close()

	result, err := invoke.Invoke(ctx, buildInvokeParams(conn, cc, store))
	exitOnCallError(err)
	exitOnEnvelopeError(result.Envelope)
	printCallOutput(cc.serverName, cc.toolName, result.Envelope, mode)
}

func openCallStore(cfg *config.Config, configDir string, clock clock.Clock) *response.Store {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return mustCallStore(cfg, configDir, logger, clock)
}

func buildInvokeParams(conn transport.Connection, cc callContext, store *response.Store) invoke.InvokeParams {
	return invoke.InvokeParams{
		Server:   cc.serverName,
		Tool:     cc.toolName,
		Params:   cc.params,
		Conn:     conn,
		ProjCfg:  resolveCallProjection(cc.sc, cc.toolName),
		ProjDefs: projection.DefaultsFrom(cc.cfg),
		Builder:  response.NewBuilder(store),
		Clock:    cc.clock,
	}
}

func exitOnCallError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "mini: %v\n", err)
	os.Exit(1)
}

func exitOnEnvelopeError(env *response.Envelope) {
	if env.Error == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "mini: tool error: %s\n", env.Message)
	os.Exit(1)
}

func parseParams(pos []string) (map[string]any, bool) {
	if len(pos) < 3 {
		return nil, true
	}
	raw, err := readParamBytes(pos[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: read stdin: %v\n", err)
		return nil, false
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		fmt.Fprintf(os.Stderr, "mini: invalid params JSON: %v\n", err)
		return nil, false
	}
	return params, true
}

func readParamBytes(arg string) ([]byte, error) {
	if arg != "-" {
		return []byte(arg), nil
	}
	return io.ReadAll(os.Stdin)
}

func resolveCallOutput(f callFlags, cfgFormat string) callOutput {
	switch {
	case f.raw:
		return callOutputRaw
	case f.toon:
		return callOutputToon
	case f.json:
		return callOutputJSON
	case cfgFormat == config.FormatToon:
		return callOutputToon
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

func mustCallStore(cfg *config.Config, configDir string, logger *slog.Logger, clock clock.Clock) *response.Store {
	sc := response.StoreConfigFrom(cfg, configDir)
	sc.Clock = clock
	store, err := response.NewStore(sc)
	if err != nil {
		logger.Warn("could not open response store, using temp dir", "err", err)
		sc.Dir = filepath.Join(os.TempDir(), "mini-responses")
		store, _ = response.NewStore(sc)
	}
	return store
}

func printCallOutput(serverName, toolName string, env *response.Envelope, mode callOutput) {
	if mode == callOutputToon {
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})).With("server", serverName, "tool", toolName)
		fmt.Println(server.EncodeToon(logger, env))
		return
	}
	b, _ := json.MarshalIndent(env, "", "  ")
	fmt.Println(string(b))
}
