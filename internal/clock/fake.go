//go:build test

package clock

import (
	"context"
	"sync"
	"time"
)

// Fake is a Clock whose time only advances when Advance is called.
// Use BlockUntilContext before Advance to ensure goroutines have registered
// their timers — otherwise you race with the goroutine scheduler.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	notify chan struct{} // closed and replaced each time a timer is registered
}

// NewFake returns a Fake clock starting at t.
func NewFake(t time.Time) *Fake {
	return &Fake{now: t, notify: make(chan struct{})}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer registers a timer that fires when Advance moves the clock past its deadline.
func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	ft := &fakeTimer{ch: make(chan time.Time, 1), deadline: f.now.Add(d), clock: f}
	f.timers = append(f.timers, ft)
	close(f.notify)
	f.notify = make(chan struct{})
	f.mu.Unlock()
	return ft
}

// Advance moves the clock forward by d, firing any timers whose deadlines have passed.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	var fired, remaining []*fakeTimer
	for _, t := range f.timers {
		if !f.now.Before(t.deadline) {
			fired = append(fired, t)
		} else {
			remaining = append(remaining, t)
		}
	}
	f.timers = remaining
	f.mu.Unlock()
	for _, t := range fired {
		t.ch <- t.deadline
	}
}

// BlockUntilContext blocks until at least n timers are pending or ctx is canceled.
// Call before Advance to guarantee goroutines have registered their timers.
func (f *Fake) BlockUntilContext(ctx context.Context, n int) error {
	for {
		f.mu.Lock()
		if len(f.timers) >= n {
			f.mu.Unlock()
			return nil
		}
		ch := f.notify
		f.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type fakeTimer struct {
	ch       chan time.Time
	deadline time.Time
	clock    *Fake
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	for i, ft := range t.clock.timers {
		if ft == t {
			t.clock.timers = append(t.clock.timers[:i], t.clock.timers[i+1:]...)
			return true
		}
	}
	return false
}
