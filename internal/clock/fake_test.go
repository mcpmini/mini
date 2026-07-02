//go:build test

package clock

import (
	"testing"
	"time"
)

func TestFakeTicker_firesAtEachInterval(t *testing.T) {
	f := NewFake()
	d := 100 * time.Millisecond
	ticker := f.NewTicker(d)

	// Advance by 3.5 intervals — channel buffer is 1, so exactly 1 tick is deliverable;
	// excess ticks are dropped via select-default, matching real time.Ticker semantics.
	f.Advance(350 * time.Millisecond)

	var got int
outer:
	for {
		select {
		case <-ticker.Chan():
			got++
		default:
			break outer
		}
	}
	if got != 1 {
		t.Errorf("expected exactly 1 tick from 350ms advance at 100ms interval, got %d", got)
	}

	ft := ticker.(*fakeTicker)
	want := defaultEpoch.Add(4 * d)
	if !ft.nextFire.Equal(want) {
		t.Errorf("nextFire = %v, want %v", ft.nextFire, want)
	}
}
