package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

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

func runTest(configDir string, args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	timeout := fs.Duration("timeout", 30*time.Second, "per-upstream connect timeout")
	fs.Parse(args)
	ctx := context.Background()
	srv, enabled := buildTestServer(ctx, configDir)
	defer srv.Close()
	printTestResults(checkUpstreams(ctx, srv, enabled, *timeout))
}

func buildTestServer(ctx context.Context, configDir string) (*server.Server, []config.ServerConfig) {
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	injectOAuthTokens(ctx, configDir, servers)
	enabled := enabledServers(servers)
	if len(enabled) == 0 {
		fmt.Println("no servers configured")
		os.Exit(0)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return server.NewWithConfigDir(cfg, configDir, logger), enabled
}

func enabledServers(servers []config.ServerConfig) []config.ServerConfig {
	out := make([]config.ServerConfig, 0, len(servers))
	for _, sc := range servers {
		if sc.IsEnabled() {
			out = append(out, sc)
		}
	}
	return out
}

func checkUpstreams(ctx context.Context, srv *server.Server, servers []config.ServerConfig, timeout time.Duration) []upstreamResult {
	results := make([]upstreamResult, len(servers))
	for i, sc := range servers {
		results[i] = probeUpstream(ctx, srv, sc, timeout)
	}
	return results
}

func probeUpstream(ctx context.Context, srv *server.Server, sc config.ServerConfig, timeout time.Duration) upstreamResult {
	appClock := clock.System()
	tctx, cancel := context.WithTimeout(ctx, timeout)
	start := appClock.Now()
	err := srv.AddUpstream(tctx, sc)
	elapsed := appClock.Since(start)
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
		fmt.Fprintf(w, "FAIL\t%s\t%s\t%v\n", r.name, displayTransport(r.transport), r.err)
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
