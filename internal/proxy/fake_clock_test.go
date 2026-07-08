//go:build test

package proxy

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func runWithFakeClock(t testHelper, p RunParams) error {
	t.Helper()
	deadline := time.After(time.Second)
	clk := clock.NewFake()
	p.Clock = clk
	done := make(chan error, 1)
	go func() { done <- Run(p) }()
	for {
		select {
		case err := <-done:
			return err
		default:
		}
		if timerRegistered(clk) {
			clk.Advance(time.Hour)
			continue
		}
		select {
		case err := <-done:
			return err
		case <-deadline:
			return context.DeadlineExceeded
		case <-time.After(time.Millisecond):
		}
	}
}

func timerRegistered(clk *clock.Fake) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	return clk.BlockUntilContext(ctx, 1) == nil
}

type testHelper interface {
	Helper()
}
