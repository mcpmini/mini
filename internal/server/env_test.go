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
	offset := time.Until(deadline)
	if offset < 29*time.Second || offset > 31*time.Second {
		t.Fatalf("expected ~30s timeout from start, got %v", offset)
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
	offset := time.Until(deadline)
	if offset <= 0 || offset > time.Second {
		t.Fatalf("expected short timeout from start, got %v", offset)
	}
}
