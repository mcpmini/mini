package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

type upstreamResult struct {
	name      string
	transport config.TransportKind
	tools     int
	elapsed   time.Duration
	err       error
}

func newTestCmd(opts *rootOptions) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test",
		Short: "CI-safe health check (exits 1 on any failure)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTest(opts.configDir, timeout, cmd.OutOrStdout())
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "per-upstream connect timeout")
	return cmd
}

func runTest(configDir string, timeout time.Duration, out io.Writer) error {
	ctx := context.Background()
	srv, servers, err := buildTestServer(ctx, configDir)
	if err != nil {
		return err
	}
	defer srv.Close()
	if !slices.ContainsFunc(servers.Loaded, config.ServerConfig.IsEnabled) && !servers.HasProblems() {
		_, err := fmt.Fprintln(out, emptyTestMessage(servers.Loaded))
		return err
	}
	results := brokenServerResults(servers.Broken)
	return printTestResults(
		out,
		append(
			results,
			checkServers(checkServersParams{Context: ctx, Server: srv, Servers: servers.Loaded, Timeout: timeout})...),
	)
}

func emptyTestMessage(servers []config.ServerConfig) string {
	if len(servers) == 0 {
		return noServersConfigured
	}
	return "no enabled servers"
}

func buildTestServer(ctx context.Context, configDir string) (*server.Server, config.Servers, error) {
	cfg, servers, err := loadConfig(configDir)
	if err != nil {
		return nil, config.Servers{}, err
	}
	injectOAuthTokens(ctx, configDir, servers.Loaded)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.New(server.Params{Config: cfg, ConfigDir: configDir, Logger: logger}), servers, nil
}

func brokenServerResults(broken []config.SourceError) []upstreamResult {
	results := make([]upstreamResult, len(broken))
	for i, b := range broken {
		results[i] = upstreamResult{name: b.ServerName, transport: unknownTransport, err: b.Err}
	}
	return results
}

func checkServers(params checkServersParams) []upstreamResult {
	var results []upstreamResult
	for _, sc := range params.Servers {
		if sc.IsEnabled() {
			results = append(
				results,
				checkServer(
					probeUpstreamParams{
						Context: params.Context,
						Server:  params.Server,
						Config:  sc,
						Timeout: params.Timeout,
					},
				),
			)
		} else if sc.ProjectionsErr != nil {
			results = append(results, upstreamResult{name: sc.Name, transport: sc.Transport, err: projectionsError(sc)})
		}
	}
	return results
}

func checkServer(params probeUpstreamParams) upstreamResult {
	r := probeUpstream(params)
	if r.err == nil && params.Config.ProjectionsErr != nil {
		r.err = projectionsError(params.Config)
	}
	return r
}

func probeUpstream(params probeUpstreamParams) upstreamResult {
	clock := clock.System()
	tctx, cancel := context.WithTimeout(params.Context, params.Timeout)
	start := clock.Now()
	err := params.Server.AddUpstream(tctx, params.Config)
	elapsed := clock.Since(start)
	cancel()
	r := upstreamResult{name: params.Config.Name, transport: params.Config.Transport, elapsed: elapsed, err: err}
	if err == nil {
		r.tools = params.Server.ToolCount(params.Config.Name)
	}
	return r
}

func countResults(results []upstreamResult) (passed, failed int) {
	for _, r := range results {
		if r.err != nil {
			failed++
		} else {
			passed++
		}
	}
	return
}

func printTestResults(out io.Writer, results []upstreamResult) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, r := range results {
		if err := writeTestRow(w, r); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush test results: %w", err)
	}
	passed, failed := countResults(results)
	if _, err := fmt.Fprintf(out, "\n%d passed, %d failed\n", passed, failed); err != nil {
		return fmt.Errorf("write test result summary: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("%d server(s) failed", failed)
	}
	return nil
}

func writeTestRow(w *tabwriter.Writer, r upstreamResult) error {
	if r.err != nil {
		_, err := fmt.Fprintf(w, "FAIL\t%s\t%s\t%s\n", r.name, displayTransport(r.transport), singleLine(r.err))
		return err
	}
	_, err := fmt.Fprintf(
		w,
		"PASS\t%s\t%s\t%d tools\t(%s)\n",
		r.name,
		displayTransport(r.transport),
		r.tools,
		r.elapsed.Round(time.Millisecond),
	)
	return err
}

func displayTransport(transport config.TransportKind) string {
	if transport == "" {
		return "stdio"
	}
	return string(transport)
}

type checkServersParams struct {
	Context context.Context
	Server  *server.Server
	Servers []config.ServerConfig
	Timeout time.Duration
}

type probeUpstreamParams struct {
	Context context.Context
	Server  *server.Server
	Config  config.ServerConfig
	Timeout time.Duration
}
