//go:build test

package randutil_test

import (
	"encoding/hex"
	"testing"

	"github.com/mcpmini/mini/internal/randutil"
)

func TestBytes_returnsRequestedLength(t *testing.T) {
	for _, n := range []int{0, 1, 4, 16, 32} {
		got := randutil.Bytes(n)
		if len(got) != n {
			t.Errorf("Bytes(%d): got len %d", n, len(got))
		}
	}
}

func TestBytes_notAllZeros(t *testing.T) {
	b := randutil.Bytes(32)
	allZero := true
	for _, v := range b {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("Bytes(32) returned all zeros")
	}
}

func TestHexString_isValidHexOfCorrectLength(t *testing.T) {
	got := randutil.HexString(16)
	if len(got) != 32 {
		t.Errorf("HexString(16): want len 32, got %d", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Errorf("HexString(16): not valid hex: %v", err)
	}
}
