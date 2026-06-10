package main

import (
	"context"
	"encoding/json"
	"errors"
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
		fmt.Fprintln(os.Stderr, "usage: mini pipe <list|run|check> [flags]")
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		runPipeList(configDir)
	case "run":
		runPipeRun(configDir, rest)
	case "check":
		runPipeCheck(configDir, rest)
	default:
		fmt.Fprintf(os.Stderr, "mini pipe: unknown subcommand %q\n", sub)
		os.Exit(2)
	}
}

func runPipeList(configDir string) {
	pipeCfgs, loadErr := config.LoadPipes(configDir)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "mini: pipe load errors:\n%v\n\n", loadErr)
	}
	if len(pipeCfgs) == 0 && loadErr == nil {
		fmt.Println("no pipes loaded (drop YAML files in ~/.mini/pipes/)")
		return
	}
	if len(pipeCfgs) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTEPS\tDESCRIPTION")
		for _, p := range pipeCfgs {
			fmt.Fprintf(w, "%s\t%d\t%s\n", p.Name, len(p.Steps), p.Description)
		}
		w.Flush()
	}
	if loadErr != nil {
		os.Exit(1)
	}
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
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: marshal result: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
	if !result.OK {
		os.Exit(1)
	}
}

// errPipeRanOnDaemon marks an error that occurred after the daemon already
// executed the pipe — callers must not fall back to direct execution, since
// that would re-run any side-effecting steps.
type errPipeRanOnDaemon struct{ err error }

func (e *errPipeRanOnDaemon) Error() string { return e.err.Error() }
func (e *errPipeRanOnDaemon) Unwrap() error { return e.err }

func pipeExec(ctx context.Context, configDir, pipeName string, inputs map[string]any) *pipes.Result {
	if port := daemon.RunningPort(configDir); port != 0 {
		result, err := daemonPipeExec(ctx, port, pipeName, inputs)
		if err == nil {
			return result
		}
		var ranOnDaemon *errPipeRanOnDaemon
		if errors.As(err, &ranOnDaemon) {
			fmt.Fprintf(os.Stderr, "mini: pipe ran on daemon but its result could not be read: %v\n", err)
			os.Exit(1)
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
	return callPipeViaMCP(ctx, conn, pipeName, inputs)
}

func callPipeViaMCP(ctx context.Context, conn transport.Connection, pipeName string, inputs map[string]any) (*pipes.Result, error) {
	params, err := json.Marshal(transport.ToolCallParams{Name: "call", Arguments: map[string]any{
		"server": config.UserServerName,
		"tool":   pipeName,
		"params": inputs,
	}})
	if err != nil {
		return nil, err
	}
	raw, err := conn.Call(ctx, "tools/call", json.RawMessage(params))
	if err != nil {
		return nil, err
	}
	extracted, err := invoke.ExtractContent(raw)
	if err != nil {
		return nil, &errPipeRanOnDaemon{err}
	}
	var result pipes.Result
	if err := json.Unmarshal(extracted, &result); err != nil {
		return nil, &errPipeRanOnDaemon{err}
	}
	return &result, nil
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
	return cp.Execute(ctx, inputs, caller)
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

func runPipeCheck(configDir string, args []string) {
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	pipeCfgs, err := config.LoadPipes(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mini: some pipes failed to load:\n%v\n", err)
	}
	if len(pipeCfgs) == 0 {
		fmt.Println("no pipes found")
		return
	}
	checkPipes(selectPipesToCheck(pipeCfgs, name))
}

func selectPipesToCheck(pipeCfgs []config.PipeConfig, name string) []config.PipeConfig {
	if name == "" {
		return pipeCfgs
	}
	p := findPipeByName(pipeCfgs, name)
	if p == nil {
		fmt.Fprintf(os.Stderr, "mini: pipe %q not found\n", name)
		os.Exit(1)
	}
	return []config.PipeConfig{*p}
}

func checkPipes(toCheck []config.PipeConfig) {
	exitCode := 0
	for _, p := range toCheck {
		if _, err := pipes.Compile(p); err != nil {
			fmt.Fprintf(os.Stderr, "  ERROR %s: %v\n", p.Name, err)
			exitCode = 1
		} else {
			fmt.Printf("  OK    %s\n", p.Name)
		}
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
