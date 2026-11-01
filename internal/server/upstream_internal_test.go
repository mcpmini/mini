// White-box tests for upstream connection error helpers.
package server

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsConnError_true(t *testing.T) {
	err := connError{err: errors.New("broken pipe")}
	if !isConnError(err) {
		t.Error("expected isConnError=true for connError")
	}
}

func TestIsConnError_false(t *testing.T) {
	if isConnError(errors.New("regular error")) {
		t.Error("expected isConnError=false for regular error")
	}
}

func TestIsConnError_wrapped(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", connError{err: errors.New("inner")})
	if !isConnError(wrapped) {
		t.Error("expected isConnError=true for wrapped connError")
	}
}

func TestIsConnError_nil(t *testing.T) {
	if isConnError(nil) {
		t.Error("expected isConnError=false for nil")
	}
}

func TestConnError_unwrap(t *testing.T) {
	inner := errors.New("inner error")
	ce := connError{err: inner}
	if ce.Error() != "inner error" {
		t.Errorf("Error() = %q, want %q", ce.Error(), "inner error")
	}
	if !errors.Is(ce, inner) {
		t.Error("expected errors.Is to find inner error via Unwrap")
	}
}
