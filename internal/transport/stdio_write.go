package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type deadlineWriter interface{ SetWriteDeadline(time.Time) error }

func (c *StdioConnection) writeJSONContext(ctx context.Context, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := c.lockWrite(ctx); err != nil {
		return err
	}
	defer c.unlockWrite()
	if err := ctx.Err(); err != nil {
		return err
	}
	stopInterrupt := c.interruptWriteOnCancel(ctx)
	_, writeErr := fmt.Fprintf(c.stdin, "%s\n", encoded)
	if err := stopInterrupt(); err != nil {
		return err
	}
	if writeErr != nil {
		return fmt.Errorf("write to upstream: %w", writeErr)
	}
	return nil
}

func (c *StdioConnection) interruptWriteOnCancel(ctx context.Context) (stop func() error) {
	pipe, ok := c.stdin.(deadlineWriter)
	if !ok {
		return func() error { return nil }
	}
	interrupted := make(chan error, 1)
	stopAfter := context.AfterFunc(ctx, func() {
		// A child that stops reading stdin blocks the write once the pipe fills; a past deadline
		// unblocks it without killing the child.
		err := pipe.SetWriteDeadline(time.Unix(1, 0))
		interrupted <- err
		if err != nil {
			c.abandon() // ending the connection closes stdin, which fails the blocked write
		}
	})
	return func() error {
		if stopAfter() {
			return nil
		}
		if err := <-interrupted; err != nil {
			return fmt.Errorf("interrupt upstream write: %w", err)
		}
		if err := pipe.SetWriteDeadline(time.Time{}); err != nil {
			return fmt.Errorf("reset upstream write deadline: %w", err)
		}
		return nil
	}
}

func (c *StdioConnection) lockWrite(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.writeGate:
		return nil
	}
}

func (c *StdioConnection) unlockWrite() { c.writeGate <- struct{}{} }

func (c *StdioConnection) abandonAfterFailedWrite(ctx context.Context, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
		return // the write never started
	}
	c.abandon() // a partial line would break the framing of every later message
}

// abandon ends the connection without Close's listener join, so the listener goroutine can call it.
func (c *StdioConnection) abandon() {
	c.closeDone()
	_ = c.shutdownProcess() //nolint:errcheck // the caller reports why the connection ended
}
