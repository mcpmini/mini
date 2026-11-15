//go:build test

package invoke

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestWithHandshakeTimeout_onTheSystemClock_isADeadlineTheTransportCanBudget(t *testing.T) {
	ctx, cancel, _ := withHandshakeTimeout(t.Context(), nil, "2s")
	defer cancel()

	deadline, ok := ctx.Deadline()

	if !ok || time.Until(deadline) > 2*time.Second {
		t.Errorf(
			"handshake ctx deadline = %v, %v; want one within 2s, which stdio's legacy fallback budgets against",
			deadline,
			ok,
		)
	}
}

func TestHandshakeTimeout_resolvesSpec(t *testing.T) {
	tests := []struct {
		name        string
		spec        string
		want        time.Duration
		wantBounded bool
	}{
		{name: "empty uses the 30s default", spec: "", want: 30 * time.Second, wantBounded: true},
		{name: "zero disables the deadline", spec: "0", wantBounded: false},
		{name: "an explicit duration bounds it", spec: "3s", want: 3 * time.Second, wantBounded: true},
		{
			name:        "an invalid spec falls back to the default, not unlimited",
			spec:        "bogus",
			want:        30 * time.Second,
			wantBounded: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, bounded := handshakeTimeout(tc.spec)
			if bounded != tc.wantBounded || (bounded && got != tc.want) {
				t.Errorf("handshakeTimeout(%q) = %v, %v; want %v, %v", tc.spec, got, bounded, tc.want, tc.wantBounded)
			}
		})
	}
}

func TestMarkOAuthIfRequired_secondCallSkipsRewrite(t *testing.T) {
	configDir := t.TempDir()
	p := DialParams{ConfigDir: configDir, Server: config.ServerConfig{
		Name: "svc", Transport: "http", URL: "https://example.com/mcp",
	}}.withDefaults()
	connErr := fmt.Errorf("list tools: %w", &transport.UnauthorizedError{WWWAuthenticate: "Bearer"})

	if err := markOAuthIfRequired(context.Background(), p, connErr); err == nil ||
		!strings.Contains(err.Error(), "requires OAuth authorization") {
		t.Fatalf("first call: err = %v, want oauth-required error", err)
	}

	// Make a rewrite attempt fail: without the fix, the second call would try
	// MarkOAuthDetected again, hit this permission error, and fall back to returning
	// the bare connErr — so the error content alone proves whether it short-circuited.
	metaPath := config.ServerMetaPath(configDir, "svc")
	if err := os.Chmod(metaPath, 0o400); err != nil {
		t.Fatalf("chmod marker read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(metaPath, 0o600) }) //nolint:errcheck

	err := markOAuthIfRequired(context.Background(), p, connErr)
	if err == nil || !strings.Contains(err.Error(), "requires OAuth authorization") {
		t.Errorf(
			"second call: err = %v, want the oauth-required error (a rewrite attempt would have failed and returned the bare connErr instead)",
			err,
		)
	}
}
