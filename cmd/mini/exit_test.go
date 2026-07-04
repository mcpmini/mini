//go:build test

package main

import (
	"errors"
	"testing"
)

func TestExitCodeFor(t *testing.T) {
	t.Run("exitError carries its own code", func(t *testing.T) {
		if got := exitCodeFor(usageErrf("bad usage")); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})
	t.Run("plain error defaults to 1", func(t *testing.T) {
		if got := exitCodeFor(errors.New("boom")); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})
	t.Run("cobra unknown command falls back to 2", func(t *testing.T) {
		if got := exitCodeFor(errors.New(`unknown command "bogus" for "mini"`)); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})
}
