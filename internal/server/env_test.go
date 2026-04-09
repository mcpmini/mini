package server

import (
	"context"
	"testing"
	"time"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("MINI_TOKEN", "secret")
	got := expandEnv("Bearer ${MINI_TOKEN} and $MINI_TOKEN")
	want := "Bearer secret and secret"
	if got != want {
		t.Fatalf("expandEnv() = %q, want %q", got, want)
	}
}

func TestApplyToolTimeoutDefault(t *testing.T) {
	ctx, cancel := applyToolTimeout(context.Background(), "")
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected default timeout deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 29*time.Second || remaining > 31*time.Second {
		t.Fatalf("expected ~30s timeout, got %v", remaining)
	}
}

func TestApplyToolTimeoutDisabledByZero(t *testing.T) {
	base := context.Background()
	ctx, cancel := applyToolTimeout(base, "0")
	defer cancel()

	if ctx != base {
		t.Fatal("expected zero timeout to reuse parent context")
	}
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("expected zero timeout to have no deadline")
	}
}

func TestApplyToolTimeoutRejectsInvalidOrNonPositiveDurations(t *testing.T) {
	for _, spec := range []string{"bogus", "-1s"} {
		t.Run(spec, func(t *testing.T) {
			base := context.Background()
			ctx, cancel := applyToolTimeout(base, spec)
			defer cancel()

			if ctx != base {
				t.Fatalf("expected invalid spec %q to reuse parent context", spec)
			}
			if _, ok := ctx.Deadline(); ok {
				t.Fatalf("expected invalid spec %q to have no deadline", spec)
			}
		})
	}
}

func TestApplyToolTimeoutUsesExplicitDuration(t *testing.T) {
	ctx, cancel := applyToolTimeout(context.Background(), "50ms")
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected explicit duration deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 200*time.Millisecond {
		t.Fatalf("expected short timeout, got %v", remaining)
	}
}
