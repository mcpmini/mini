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
