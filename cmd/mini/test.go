package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
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
	transport string
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
			runTest(opts.configDir, timeout)
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "per-upstream connect timeout")
	return cmd
}

func runTest(configDir string, timeout time.Duration) {
	ctx := context.Background()
	srv, servers := buildTestServer(ctx, configDir)
	defer srv.Close()
	results := brokenServerResults(servers.Broken)
	printTestResults(append(results, checkServers(ctx, srv, servers.Loaded, timeout)...))
}

func emptyTestMessage(servers []config.ServerConfig) string {
	if len(servers) == 0 {
		return noServersConfigured
	}
	return "no enabled servers"
}

func buildTestServer(ctx context.Context, configDir string) (*server.Server, config.Servers) {
	cfg, servers, err := loadConfig(configDir)
	if err != nil {
		fatalf("%v", err)
	}
	injectOAuthTokens(ctx, configDir, servers.Loaded)
	if !slices.ContainsFunc(servers.Loaded, config.ServerConfig.IsEnabled) && !servers.HasProblems() {
		fmt.Println(emptyTestMessage(servers.Loaded))
		os.Exit(0)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.New(server.Params{Config: cfg, ConfigDir: configDir, Logger: logger}), servers
}

func brokenServerResults(broken []config.SourceError) []upstreamResult {
	results := make([]upstreamResult, len(broken))
	for i, b := range broken {
		results[i] = upstreamResult{name: b.ServerName, transport: unknownTransport, err: b.Err}
	}
	return results
}

func checkServers(ctx context.Context, srv *server.Server, servers []config.ServerConfig, timeout time.Duration) []upstreamResult {
	var results []upstreamResult
	for _, sc := range servers {
		if sc.IsEnabled() {
			results = append(results, checkServer(ctx, srv, sc, timeout))
		} else if sc.ProjectionsErr != nil {
			results = append(results, upstreamResult{name: sc.Name, transport: sc.Transport, err: projectionsError(sc)})
		}
	}
	return results
}

func checkServer(ctx context.Context, srv *server.Server, sc config.ServerConfig, timeout time.Duration) upstreamResult {
	r := probeUpstream(ctx, srv, sc, timeout)
	if r.err == nil && sc.ProjectionsErr != nil {
		r.err = projectionsError(sc)
	}
	return r
}

func projectionsError(sc config.ServerConfig) error {
	return fmt.Errorf("projections: %w", sc.ProjectionsErr.Err)
}

func probeUpstream(ctx context.Context, srv *server.Server, sc config.ServerConfig, timeout time.Duration) upstreamResult {
	clock := clock.System()
	tctx, cancel := context.WithTimeout(ctx, timeout)
	start := clock.Now()
	err := srv.AddUpstream(tctx, sc)
	elapsed := clock.Since(start)
	cancel()
	r := upstreamResult{name: sc.Name, transport: sc.Transport, elapsed: elapsed, err: err}
	if err == nil {
		r.tools = srv.ToolCount(sc.Name)
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

func printTestResults(results []upstreamResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range results {
		writeTestRow(w, r)
	}
	w.Flush()
	passed, failed := countResults(results)
	fmt.Printf("\n%d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func writeTestRow(w *tabwriter.Writer, r upstreamResult) {
	if r.err != nil {
		fmt.Fprintf(w, "FAIL\t%s\t%s\t%s\n", r.name, displayTransport(r.transport), singleLine(r.err))
	} else {
		fmt.Fprintf(w, "PASS\t%s\t%s\t%d tools\t(%s)\n", r.name, displayTransport(r.transport), r.tools, r.elapsed.Round(time.Millisecond))
	}
}

func displayTransport(transport string) string {
	if transport == "" {
		return "stdio"
	}
	return transport
}
