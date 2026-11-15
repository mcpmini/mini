//go:build test

package server

import (
	"testing"
	"time"
)

func TestParseToolTimeout_default(t *testing.T) {
	d, ok := parseToolTimeout("")
	if !ok {
		t.Fatal("expected default to be enabled")
	}
	if d != 30*time.Second {
		t.Fatalf("expected 30s default, got %v", d)
	}
}

func TestParseToolTimeout_explicit(t *testing.T) {
	d, ok := parseToolTimeout("50ms")
	if !ok {
		t.Fatal("expected explicit duration to be enabled")
	}
	if d != 50*time.Millisecond {
		t.Fatalf("expected 50ms, got %v", d)
	}
}

func TestParseToolTimeout_disabledByZero(t *testing.T) {
	_, ok := parseToolTimeout("0")
	if ok {
		t.Fatal("expected zero to disable timeout")
	}
}

func TestParseToolTimeout_invalidSpec(t *testing.T) {
	for _, spec := range []string{"bogus", "-1s"} {
		t.Run(spec, func(t *testing.T) {
			_, ok := parseToolTimeout(spec)
			if ok {
				t.Fatalf("expected invalid spec %q to disable timeout", spec)
			}
		})
	}
}
