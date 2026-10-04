package clock

import (
	"context"
	"time"
)

// WithTimeout is context.WithTimeout with the deadline on c, so tests can expire it with a fake
// clock instead of waiting it out.
func WithTimeout(parent context.Context, c Clock, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	deadline := c.NewTimer(d)
	go func() {
		select {
		case <-deadline.Chan():
			cancel()
		case <-ctx.Done():
			deadline.Stop()
		}
	}()
	return ctx, cancel
}
