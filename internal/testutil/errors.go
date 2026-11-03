package testutil

import (
	"strings"
	"testing"
)

func ErrorContains(t testing.TB, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one containing %q", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error %q does not contain %q", err, substr)
	}
}
