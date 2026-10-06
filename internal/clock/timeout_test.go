//go:build test

package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithTimeout(t *testing.T) {
	t.Run("expires when the clock passes the deadline", func(t *testing.T) {
		f := NewFake()
		ctx, cancel := WithTimeout(context.Background(), f, time.Second)
		defer cancel()
		f.Advance(999 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("expired before the deadline")
		}
		f.Advance(time.Millisecond)
		<-ctx.Done()
	})
	t.Run("follows its parent", func(t *testing.T) {
		parent, cancelParent := context.WithCancel(context.Background())
		ctx, cancel := WithTimeout(parent, NewFake(), time.Second)
		defer cancel()
		cancelParent()
		<-ctx.Done()
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Errorf("err = %v, want canceled", ctx.Err())
		}
	})
}
