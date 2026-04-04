// Package clock abstracts time so tests can control timers without real sleeps.
// Production code uses the zero value or clock.System(); tests pass clock.NewFake().
package clock

import "time"

// Clock abstracts the time operations used by this codebase.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer mirrors time.Timer but is replaceable in tests.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// System returns a Clock backed by the system clock.
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(d time.Duration) Timer {
	t := time.NewTimer(d)
	return &systemTimer{t}
}

type systemTimer struct{ t *time.Timer }

func (r *systemTimer) C() <-chan time.Time { return r.t.C }
func (r *systemTimer) Stop() bool         { return r.t.Stop() }
