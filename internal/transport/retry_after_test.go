//go:build test

package transport

import (
	"net/http"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestParseRetryAfter_seconds(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("30", now)
	if d != 30*time.Second {
		t.Errorf("expected 30s, got %v", d)
	}
}

func TestParseRetryAfter_zero(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("0", now)
	if d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
}

func TestParseRetryAfter_empty(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("", now)
	if d != -1 {
		t.Errorf("expected -1 for empty, got %v", d)
	}
}

func TestParseRetryAfter_httpDate_future(t *testing.T) {
	now := clock.NewFake().Now()
	future := now.Add(5 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(future, now)
	if d <= 0 {
		t.Errorf("expected positive duration for future date, got %v", d)
	}
}

func TestParseRetryAfter_httpDate_past(t *testing.T) {
	now := clock.NewFake().Now()
	past := now.Add(-5 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(past, now)
	if d != -1 {
		t.Errorf("expected -1 for past date, got %v", d)
	}
}

func TestParseRetryAfter_invalid(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("not-a-date", now)
	if d != -1 {
		t.Errorf("expected -1 for invalid value, got %v", d)
	}
}
