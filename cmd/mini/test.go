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
	cfg, servers, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	ctx := context.Background()
	injectOAuthTokens(ctx, configDir, servers)
	enabled := enabledServers(servers)
	if len(enabled) == 0 {
		fmt.Println("no servers configured")
		os.Exit(0)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.NewWithConfigDir(cfg, configDir, logger)
	defer srv.Close()
	results := checkUpstreams(ctx, srv, enabled, *timeout)
	printTestResults(results)
}

func enabledServers(servers []config.ServerConfig) []config.ServerConfig {
	out := make([]config.ServerConfig, 0, len(servers))
	for _, sc := range servers {
		if isEnabled(sc) {
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
	tctx, cancel := context.WithTimeout(ctx, timeout)
	start := time.Now()
	err := srv.AddUpstream(tctx, sc)
	elapsed := time.Since(start)
	cancel()
	r := upstreamResult{name: sc.Name, transport: sc.Transport, elapsed: elapsed, err: err}
	if err == nil {
		r.tools = srv.ToolCount(sc.Name)
	}
	return r
}

func printTestResults(results []upstreamResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	passed, failed := 0, 0
	for _, r := range results {
		t := r.transport
		if t == "" {
			t = "stdio"
		}
		if r.err != nil {
			fmt.Fprintf(w, "FAIL\t%s\t%s\t%v\n", r.name, t, r.err)
			failed++
		} else {
			fmt.Fprintf(w, "PASS\t%s\t%s\t%d tools\t(%s)\n", r.name, t, r.tools, r.elapsed.Round(time.Millisecond))
			passed++
		}
	}
	w.Flush()
	fmt.Printf("\n%d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
