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
	mu      sync.Mutex
	now     time.Time
	timers  []*fakeTimer
	tickers []*fakeTicker
	notify  chan struct{} // closed and replaced each time a timer or ticker is registered
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

func (f *Fake) Since(t time.Time) time.Duration        { return f.Now().Sub(t) }
func (f *Fake) Until(t time.Time) time.Duration        { return t.Sub(f.Now()) }
func (f *Fake) After(d time.Duration) <-chan time.Time { return f.NewTimer(d).Chan() }

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

// NewTicker registers a ticker that fires repeatedly as Advance moves the clock past each interval.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	ft := &fakeTicker{ch: make(chan time.Time, 1), interval: d, nextFire: f.now.Add(d), clock: f}
	f.tickers = append(f.tickers, ft)
	close(f.notify)
	f.notify = make(chan struct{})
	f.mu.Unlock()
	return ft
}

// Advance moves the clock forward by d, firing any timers and tickers whose deadlines have passed.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	fired := f.partitionTimers()
	toFire := f.advanceTickers()
	f.mu.Unlock()
	for _, t := range fired {
		t.ch <- t.deadline
	}
	for _, item := range toFire {
		select {
		case item.ticker.ch <- item.fireTime:
		default:
		}
	}
}

func (f *Fake) partitionTimers() []*fakeTimer {
	var fired, remaining []*fakeTimer
	for _, t := range f.timers {
		if !f.now.Before(t.deadline) {
			fired = append(fired, t)
		} else {
			remaining = append(remaining, t)
		}
	}
	f.timers = remaining
	return fired
}

type tickerFire struct {
	ticker   *fakeTicker
	fireTime time.Time
}

func (f *Fake) advanceTickers() []tickerFire {
	var toFire []tickerFire
	for _, tk := range f.tickers {
		if tk.stopped || f.now.Before(tk.nextFire) {
			continue
		}
		toFire = append(toFire, tickerFire{ticker: tk, fireTime: tk.nextFire})
		for !f.now.Before(tk.nextFire) {
			tk.nextFire = tk.nextFire.Add(tk.interval)
		}
	}
	return toFire
}

// BlockUntilContext blocks until at least n timers or tickers are pending or ctx is canceled.
// Call before Advance to guarantee goroutines have registered their timers.
func (f *Fake) BlockUntilContext(ctx context.Context, n int) error {
	for {
		f.mu.Lock()
		if len(f.timers)+len(f.tickers) >= n {
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

func (t *fakeTimer) Chan() <-chan time.Time { return t.ch }

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

type fakeTicker struct {
	ch       chan time.Time
	interval time.Duration
	nextFire time.Time
	stopped  bool
	clock    *Fake
}

func (t *fakeTicker) Chan() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.stopped = true
	for i, ft := range t.clock.tickers {
		if ft == t {
			t.clock.tickers = append(t.clock.tickers[:i], t.clock.tickers[i+1:]...)
			return
		}
	}
}
