//go:build test

package server

import (
	"errors"
	"net/http"
	"testing"
	"uuid"

	"github.com/mcpmini/mini/internal/transport"
)

func TestNewSessionID_unique(t *testing.T) {
	a, b := transport.NewSessionID(), transport.NewSessionID()
	if a == "" || b == "" {
		t.Error("expected non-empty session IDs")
	}
	if a == b {
		t.Errorf("expected unique session IDs, got %q twice", a)
	}
	for _, id := range []string{a, b} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || parsed[6]>>4 != 4 {
			t.Errorf("session ID = %q, want canonical UUID v4", id)
		}
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

func TestIsSameHost_matching(t *testing.T) {
	r := &http.Request{Host: "127.0.0.1:4857"}
	cases := []struct {
		origin string
		want   bool
	}{
		{"http://127.0.0.1:4857", true},
		{"https://127.0.0.1:4857", true},
		{"http://evil.com", false},
		{"http://127.0.0.1:9999", false},
	}
	for _, c := range cases {
		if got := isSameHost(r, c.origin); got != c.want {
			t.Errorf("isSameHost(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}
