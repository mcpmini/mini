//go:build test

package forge_test

import (
	"os/exec"
	"testing"

	"github.com/mcpmini/mini/internal/forge"
)

func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
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
