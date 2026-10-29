package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

const callLong = "PARAMS is a JSON object of tool arguments, or - to read it from stdin. `mini ls SERVER TOOL` lists a tool's arguments."

const callExample = `  mini call deepwiki read_wiki_structure '{"repoName": "facebook/react"}'
  echo '{"repoName": "facebook/react"}' | mini call deepwiki read_wiki_structure -`

func newCallCmd(opts *rootOptions) *cobra.Command {
	f := callFlags{}
	cmd := &cobra.Command{
		Use:     "call SERVER TOOL [PARAMS]",
		Short:   "Invoke a tool directly (exit 1 on tool error)",
		Long:    callLong,
		Example: callExample,
		Args:    usageArgs(cobra.RangeArgs(2, 3)),
		PreRunE: func(*cobra.Command, []string) error {
			if f.enabledCount() > 1 {
				return usageErrf("choose only one output mode: --json, --toon, or --raw")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			runCallCmd(runCallParams{
				configDir: opts.configDir, args: args, flags: f,
			})
			return nil
		},
	}
	addCallOutputFlags(cmd, &f)
	return cmd
}

func addCallOutputFlags(cmd *cobra.Command, f *callFlags) {
	cmd.Flags().BoolVarP(&f.json, "json", "j", false, "JSON output (projected envelope, default)")
	cmd.Flags().BoolVarP(&f.toon, "toon", "t", false, "TOON format (token-oriented object notation)")
	cmd.Flags().BoolVarP(&f.raw, "raw", "r", false, "raw upstream response, no projection")
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

type runCallParams struct {
	configDir string
	args      []string
	flags     callFlags
}

type executeProjectedParams struct {
	ctx  context.Context
	conn transport.Connection
	call callContext
	mode callOutput
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

func runCallCmd(p runCallParams) {
	cc := parseCallContext(p.configDir, p.args)
	cc.clock = clock.System()
	exitIfHidden(cc.sc, cc.toolName)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	conn := mustDialCall(ctx, p.configDir, cc)
	defer conn.Close() //nolint:errcheck // The call result is authoritative; closing a killed stdio process may report its exit status.

	projCfg := resolveCallProjection(cc.sc, cc.toolName)
	projFormat := config.ProjectionFormat(projCfg)
	mode := resolveCallOutput(p.flags, projFormat, cc.cfg.ResponseFormat)
	if mode == callOutputRaw {
		executeRaw(ctx, conn, cc)
		return
	}
	executeProjected(executeProjectedParams{ctx: ctx, conn: conn, call: cc, mode: mode})
}

func parseCallContext(configDir string, args []string) callContext {
	params, ok := parseParams(args)
	if !ok {
		os.Exit(2)
	}
	serverName, toolName := args[0], args[1]
	cfg, sc := loadCallCtx(configDir, serverName)
	return callContext{
		cfg:        cfg,
		sc:         sc,
		configDir:  configDir,
		serverName: serverName,
		toolName:   toolName,
		params:     params,
	}
}

func loadCallCtx(configDir, serverName string) (*config.Config, *config.ServerConfig) {
	cfg, sc, err := loadOneServer(configDir, serverName, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: %v\n", err)
		os.Exit(2)
	}
	return cfg, sc
}

func exitIfHidden(sc *config.ServerConfig, toolName string) {
	if sc.Permissions.Hides(toolName) {
		fmt.Fprintf(os.Stderr, "mini: tool not found: %s\n", toolName)
		os.Exit(1)
	}
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
	raw, _, err := invoke.InvokeRaw(
		ctx,
		invoke.InvokeRawParams{
			Clock: cc.clock, Conn: conn, Server: cc.serverName, Tool: cc.toolName, Params: cc.params,
		},
	)
	recordAuthorizationFailure(cc.configDir, *cc.sc, err)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(raw))
}

func executeProjected(p executeProjectedParams) {
	store, err := openCallStore(p.call.cfg, p.call.configDir, p.call.clock)
	if err != nil {
		if closeErr := p.conn.Close(); closeErr != nil {
			err = fmt.Errorf("%w (close upstream: %w)", err, closeErr)
		}
		exitOnCallError(err)
		return
	}
	defer store.Close()

	result, err := invoke.Invoke(p.ctx, buildInvokeParams(p.conn, p.call, store))
	recordAuthorizationFailure(p.call.configDir, *p.call.sc, err)
	exitOnCallError(err)
	exitOnEnvelopeError(result.Envelope)
	exitOnCallError(printCallOutput(result.Envelope, p.mode))
}

func openCallStore(cfg *config.Config, configDir string, clock clock.Clock) (*response.Store, error) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return newCallStore(callStoreParams{
		Config:    cfg,
		ConfigDir: configDir,
		Logger:    logger,
		Clock:     clock,
	})
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
		fmt.Fprintf(os.Stderr, "mini: PARAMS must be a JSON object such as '{\"name\": \"value\"}': %v\n", err)
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

func resolveCallOutput(f callFlags, projFormat, cfgFormat config.ResponseFormat) callOutput {
	if f.raw {
		return callOutputRaw
	}
	var explicit config.ResponseFormat
	if f.toon {
		explicit = config.FormatToon
	} else if f.json {
		explicit = config.FormatJSON
	}
	if config.EffectiveFormat(explicit, projFormat, cfgFormat) == config.FormatToon {
		return callOutputToon
	}
	return callOutputJSON
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

func printCallOutput(env *response.Envelope, mode callOutput) error {
	if mode == callOutputToon {
		text, err := env.Toon()
		if err != nil {
			return fmt.Errorf("encode TOON response: %w", err)
		}
		fmt.Println(text)
		return nil
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON response: %w", err)
	}
	fmt.Println(string(b))
	return nil
}
