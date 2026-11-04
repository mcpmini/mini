//go:build test

package server_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/jq"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

// TestProxy_MiniRead_JQRoundTrip verifies that jq paths in __mini.excluded and
// __mini.truncated can be used directly as read() filters against the raw file.
func TestProxy_MiniRead_JQRoundTrip(t *testing.T) {
	upstream := `{"id":1,"secret":"key-123","body":"` + longString(3000) +
		`","items":[{"n":1},{"n":2},{"n":3},{"n":4},{"n":5}]}`
	cases := []struct {
		name       string
		projection map[string]any
		wantPaths  func(proxyEnvelope) []string
	}{
		{"excluded field", excludeSecret, func(env proxyEnvelope) []string { return env.Excluded }},
		{"truncated string", map[string]any{"string_limits": map[string]int{"body": 100}}, truncatedStrings},
		{"truncated array", map[string]any{"array_limits": map[string]int{"items": 2}}, truncatedArrays},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.ResponseDir = t.TempDir()
			h := newProxyHarness(t, newTestServer(t, server.Params{Config: cfg}))
			h.addUpstream("svc", textResult(upstream), "get_data")
			h.setProjection("svc", "get_data", tc.projection)

			env := parseProxyEnvelope(t, h.callText("svc__get_data", nil))
			paths := tc.wantPaths(env)
			if env.File == "" || len(paths) == 0 {
				t.Fatalf("expected __mini.file and paths from the projection, got %+v", env)
			}

			rawFile := testutil.ReadFile(t, filepath.Join(cfg.ResponseDir, env.File+".json"))
			for _, path := range paths {
				want, err := jq.Eval(context.Background(), rawFile, path)
				if err != nil {
					t.Errorf("jq.Eval(%q) on raw file: %v", path, err)
					continue
				}
				if got := h.callText("read", map[string]any{"file": env.File, "filter": path}); got != want {
					t.Errorf("read(file, %q): got %q, want %q", path, got, want)
				}
			}
		})
	}
}

func truncatedStrings(env proxyEnvelope) []string {
	var out []string
	for _, tr := range env.Truncated {
		if tr.Chars > 0 {
			out = append(out, tr.Path)
		}
	}
	return out
}

func truncatedArrays(env proxyEnvelope) []string {
	var out []string
	for _, tr := range env.Truncated {
		if tr.Items > 0 {
			out = append(out, tr.Path)
		}
	}
	return out
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(i%26)
	}
	return string(b)
}
