//go:build test

package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

// WaitForTimers waits until n timers wait on c. The bound only turns a broken wait into a failure
// instead of a hang; it never paces the code under test, which runs on c.
func WaitForTimers(t testing.TB, c *clock.Fake, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.BlockUntilContext(ctx, n); err != nil {
		t.Fatalf("waiting for %d timers on the fake clock: %v", n, err)
	}
}
