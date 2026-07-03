//go:build test

package forge_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/forge"
)

func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}
}

func requireCached(t *testing.T, specifiers ...string) {
	t.Helper()
	requireDeno(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append([]string{"cache"}, specifiers...)
	if out, err := exec.CommandContext(ctx, "deno", args...).CombinedOutput(); err != nil {
		t.Skipf("could not resolve %v (likely offline): %v\n%s", specifiers, err, out)
	}
}

func asForgeError(t *testing.T, err error) *forge.Error {
	t.Helper()
	fe, ok := err.(*forge.Error)
	if !ok {
		t.Fatalf("error type = %T, want *forge.Error", err)
	}
	return fe
}
