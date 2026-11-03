package testutil

import (
	"testing"
	"time"
)

// receiveTimeout bounds WaitForReceive. Long enough for a loaded CI runner, short enough that a
// lost signal fails the test well before its overall timeout.
const receiveTimeout = 5 * time.Second

// WaitForReceive fails the test if nothing arrives on ch within 5s; what names the
// awaited event in the failure. A closed channel counts as a receive.
func WaitForReceive[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	timer := time.NewTimer(receiveTimeout) //nolint:clocklint // a watchdog on real time; no test clock advances it
	defer timer.Stop()
	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatalf("timed out after %s waiting for %s", receiveTimeout, what)
		var zero T
		return zero
	}
}
