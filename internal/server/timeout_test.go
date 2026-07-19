//go:build test

package server

import (
	"context"
	"testing"
	"time"
)

func TestParseToolTimeout_default(t *testing.T) {
	d, ok := parseToolTimeout("")
	if !ok {
		t.Fatal("expected default to be enabled")
	}
	if d != 30*time.Second {
		t.Fatalf("expected 30s default, got %v", d)
	}
}

func TestParseToolTimeout_explicit(t *testing.T) {
	d, ok := parseToolTimeout("50ms")
	if !ok {
		t.Fatal("expected explicit duration to be enabled")
	}
	if d != 50*time.Millisecond {
		t.Fatalf("expected 50ms, got %v", d)
	}
}

func TestParseToolTimeout_disabledByZero(t *testing.T) {
	_, ok := parseToolTimeout("0")
	if ok {
		t.Fatal("expected zero to disable timeout")
	}
}

func TestParseToolTimeout_invalidSpec(t *testing.T) {
	for _, spec := range []string{"bogus", "-1s"} {
		t.Run(spec, func(t *testing.T) {
			_, ok := parseToolTimeout(spec)
			if ok {
				t.Fatalf("expected invalid spec %q to disable timeout", spec)
			}
		})
	}
}

func TestApplyConnectTimeout_resolvesDeadline(t *testing.T) {
	tests := []struct {
		name        string
		spec        string
		wantBounded bool
	}{
		{name: "empty uses 10s default", spec: "", wantBounded: true},
		{name: "zero disables the deadline", spec: "0", wantBounded: false},
		{name: "explicit duration is bounded", spec: "3s", wantBounded: true},
		{name: "invalid spec falls back to the default, not unlimited", spec: "bogus", wantBounded: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := applyConnectTimeout(context.Background(), tc.spec)
			defer cancel()
			_, hasDeadline := ctx.Deadline()
			if hasDeadline != tc.wantBounded {
				t.Fatalf("spec %q: hasDeadline = %v, want %v", tc.spec, hasDeadline, tc.wantBounded)
			}
		})
	}
}
