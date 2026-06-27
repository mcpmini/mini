// Package clock abstracts time so tests can control timers without real sleeps.
// Production code uses the zero value or clock.System(); tests pass clock.NewFake().
package clock

import "time"

// Clock abstracts the time operations used by this codebase.
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	Until(t time.Time) time.Duration
	After(d time.Duration) <-chan time.Time
	NewTimer(d time.Duration) Timer
	NewTicker(d time.Duration) Ticker
}

// Timer mirrors time.Timer but is replaceable in tests.
type Timer interface {
	Chan() <-chan time.Time
	Stop() bool
}

// Ticker mirrors time.Ticker but is replaceable in tests.
type Ticker interface {
	Chan() <-chan time.Time
	Stop()
}

// System returns a Clock backed by the system clock.
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (systemClock) Until(t time.Time) time.Duration        { return time.Until(t) }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (systemClock) NewTimer(d time.Duration) Timer {
	t := time.NewTimer(d)
	return &systemTimer{t}
}

func (systemClock) NewTicker(d time.Duration) Ticker {
	t := time.NewTicker(d)
	return &systemTicker{t}
}

type systemTimer struct{ t *time.Timer }

func (r *systemTimer) Chan() <-chan time.Time { return r.t.C }
func (r *systemTimer) Stop() bool             { return r.t.Stop() }

type systemTicker struct{ t *time.Ticker }

func (r *systemTicker) Chan() <-chan time.Time { return r.t.C }
func (r *systemTicker) Stop()                  { r.t.Stop() }
