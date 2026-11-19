package server

import (
	"context"
	"errors"

	"github.com/mcpmini/mini/internal/transport"
)

// classifyCallError marks a failed call as the connection's fault unless the call was cut short
// or the upstream answered with a JSON-RPC error; only the connection's faults call for a reconnect.
func classifyCallError(err error) error {
	var rpcErr *transport.RPCError
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &rpcErr) {
		return err
	}
	return connError{err}
}

// markConnError marks err as the connection's fault whatever it wraps: a transport error that
// timed out still means the connection is gone.
func markConnError(err error) error { return connError{err} }

type connError struct{ err error }

func (e connError) Error() string { return e.err.Error() }
func (e connError) Unwrap() error { return e.err }

func isConnError(err error) bool {
	var ce connError
	return errors.As(err, &ce)
}
