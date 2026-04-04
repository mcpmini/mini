//go:build test

package server_test

// Baseline (Apple M4 Pro, 2026-03-31 — flag regressions at 2x these numbers):
//   BenchmarkExec_inline-14      32468 ns/op   74128 B/op   138 allocs/op
//   BenchmarkExec_concurrent-14  24582 ns/op   74085 B/op   139 allocs/op

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func benchSrv(b *testing.B) *server.Server {
	b.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = b.TempDir()
	return server.New(cfg, logger)
}

func buildBenchInput(call []byte) []byte {
	p, _ := json.Marshal(map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "bench", "version": "0"},
	})
	init, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": json.RawMessage(p),
	})
	return append(append(init, '\n'), call...)
}

func BenchmarkExec_inline(b *testing.B) {
	srv := benchSrv(b)
	fake := fakeConnWithResponse("toolA", `{"content":[{"type":"text","text":"ok"}]}`)
	ctx := context.Background()
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake)

	input := buildBenchInput(callTool("call", map[string]any{
		"server": "svc", "tool": "toolA", "params": map[string]any{},
	}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.Serve(ctx, bytes.NewReader(input), io.Discard) //nolint:errcheck
	}
}

func BenchmarkExec_concurrent(b *testing.B) {
	srv := benchSrv(b)
	fake := fakeConnWithResponse("toolA", `{"content":[{"type":"text","text":"ok"}]}`)
	ctx := context.Background()
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake)

	input := buildBenchInput(callTool("call", map[string]any{
		"server": "svc", "tool": "toolA", "params": map[string]any{},
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			srv.Serve(ctx, bytes.NewReader(input), io.Discard) //nolint:errcheck
		}
	})
}
