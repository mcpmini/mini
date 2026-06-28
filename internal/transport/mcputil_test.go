package transport

import "testing"

func TestNormalizeID_float64_toInt64(t *testing.T) {
	if got := normalizeID(float64(42)); got != int64(42) {
		t.Errorf("expected int64(42), got %T(%v)", got, got)
	}
}

func TestNormalizeID_zero(t *testing.T) {
	if got := normalizeID(float64(0)); got != int64(0) {
		t.Errorf("expected int64(0), got %T(%v)", got, got)
	}
}

func TestNormalizeID_string_unchanged(t *testing.T) {
	if got := normalizeID("abc"); got != "abc" {
		t.Errorf("expected string passthrough, got %T(%v)", got, got)
	}
}

func TestNormalizeID_int64_unchanged(t *testing.T) {
	if got := normalizeID(int64(7)); got != int64(7) {
		t.Errorf("expected int64(7), got %T(%v)", got, got)
	}
}

