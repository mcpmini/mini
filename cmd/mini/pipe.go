package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/pipes"
	"github.com/mcpmini/mini/internal/transport"
)

func runPipe(configDir string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mini pipe <list|run> [flags]")
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		runPipeList(configDir)
	case "run":
		runPipeRun(configDir, rest)
	default:
		fmt.Fprintf(os.Stderr, "mini pipe: unknown subcommand %q\n", sub)
		os.Exit(2)
	}
}

func runPipeList(configDir string) {
	pipeCfgs, err := config.LoadPipes(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: load pipes: %v\n", err)
	}
	if len(pipeCfgs) == 0 {
		fmt.Println("no pipes loaded (drop YAML files in ~/.mini/pipes/)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTEPS\tDESCRIPTION")
	for _, p := range pipeCfgs {
		fmt.Fprintf(w, "%s\t%d\t%s\n", p.Name, len(p.Steps), p.Description)
	}
	w.Flush()
}

type pipeRunFlags struct {
	argsJSON string
}

func parsePipeRunFlags(args []string) (pipeRunFlags, string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: mini pipe run <name> [--args '{...}']")
		os.Exit(2)
	}
	name := args[0]
	fs := flag.NewFlagSet("pipe run", flag.ExitOnError)
	f := pipeRunFlags{}
	fs.StringVar(&f.argsJSON, "args", "{}", "pipe inputs as JSON object")
	fs.Parse(args[1:]) //nolint:errcheck
	return f, name
}

func runPipeRun(configDir string, args []string) {
	f, pipeName := parsePipeRunFlags(args)
	var inputs map[string]any
	if err := json.Unmarshal([]byte(f.argsJSON), &inputs); err != nil {
		fmt.Fprintf(os.Stderr, "mini: invalid args JSON: %v\n", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	result := pipeExec(ctx, configDir, pipeName, inputs)
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
	if !result.OK {
		os.Exit(1)
	}
}

func pipeExec(ctx context.Context, configDir, pipeName string, inputs map[string]any) *pipes.Result {
	if port := daemon.RunningPort(configDir); port != 0 {
		if result, err := daemonPipeExec(ctx, port, pipeName, inputs); err == nil {
			return result
		}
	}
	return directPipeExec(ctx, configDir, pipeName, inputs)
}

func daemonPipeExec(ctx context.Context, port int, pipeName string, inputs map[string]any) (*pipes.Result, error) {
	conn, err := transport.NewHTTPConnection(transport.HTTPConnectionConfig{
		URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
	})
	if err != nil {
		return nil, err
	}
	params, _ := json.Marshal(transport.ToolCallParams{Name: "call", Arguments: map[string]any{
		"server": config.UserServerName,
		"tool":   pipeName,
		"params": inputs,
	}})
	raw, err := conn.Call(ctx, "tools/call", json.RawMessage(params))
	if err != nil {
		return nil, err
	}
	extracted, err := invoke.ExtractContent(raw)
	if err != nil {
		return nil, err
	}
	var result pipes.Result
	return &result, json.Unmarshal(extracted, &result)
}

func loadCompiledPipe(configDir, pipeName string) *pipes.CompiledPipe {
	pipeCfgs, err := config.LoadPipes(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: load pipes: %v\n", err)
		os.Exit(1)
	}
	pipe := findPipeByName(pipeCfgs, pipeName)
	if pipe == nil {
		fmt.Fprintf(os.Stderr, "mini: pipe %q not found\n", pipeName)
		os.Exit(1)
	}
	cp, err := pipes.Compile(*pipe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: compile pipe: %v\n", err)
		os.Exit(1)
	}
	return cp
}

func directPipeExec(ctx context.Context, configDir, pipeName string, inputs map[string]any) *pipes.Result {
	cp := loadCompiledPipe(configDir, pipeName)
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: load config: %v\n", err)
		os.Exit(1)
	}
	caller := buildPipeRunCaller(ctx, configDir, cfg, servers)
	result, err := cp.Execute(ctx, inputs, caller)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: pipe error: %v\n", err)
		os.Exit(1)
	}
	return result
}

func findPipeByName(pipes []config.PipeConfig, name string) *config.PipeConfig {
	for i := range pipes {
		if pipes[i].Name == name {
			return &pipes[i]
		}
	}
	return nil
}

func buildPipeRunCaller(ctx context.Context, configDir string, cfg *config.Config, servers []config.ServerConfig) pipes.CallerFunc {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := buildAndConnectServer(ctx, cfg, configDir, logger, servers)
	return srv.MakeRawCaller(ctx)
}
