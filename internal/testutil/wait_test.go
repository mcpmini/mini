package testutil

import (
	"errors"
	"testing"
	"testing/synctest"
)

func TestRunInBackground_deliversTheFunctionsError(t *testing.T) {
	want := errors.New("boom")

	if err := WaitForReceive(t, RunInBackground(func() error { return want }), "the error"); !errors.Is(err, want) {
		t.Errorf("RunInBackground delivered %v, want %v", err, want)
	}
}

func TestRunInBackground_finishesWhenNobodyReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		RunInBackground(func() error { return nil })
	})
}

func TestWaitForReceive_returnsTheValueSent(t *testing.T) {
	ch := make(chan int, 1)
	ch <- 7

	if got := WaitForReceive(t, ch, "the value"); got != 7 {
		t.Errorf("WaitForReceive = %d, want 7", got)
	}
}

func TestWaitForReceive_aClosedChannelCountsAsReceived(t *testing.T) {
	ch := make(chan struct{})
	close(ch)

	if failure := failureOf(func(tb testing.TB) { WaitForReceive(tb, ch, "close") }); failure != "" {
		t.Errorf("WaitForReceive on a closed channel failed: %q", failure)
	}
}

func TestWaitForReceive_failsNamingWhatItWaitedFor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		never := make(chan struct{})

		failure := failureOf(func(tb testing.TB) { WaitForReceive(tb, never, "the login callback") })

		if want := "timed out after 5s waiting for the login callback"; failure != want {
			t.Errorf("failure = %q, want %q", failure, want)
		}
	})
}
