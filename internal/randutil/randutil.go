package randutil

import (
	"crypto/rand"
	"encoding/hex"
)

func Bytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b) //nolint:errcheck // Go 1.20+: crashes rather than errors when OS rand is unavailable — go.dev/issue/66821
	return b
}

// HexString returns n random bytes encoded as a lowercase hex string (length 2n).
func HexString(n int) string {
	return hex.EncodeToString(Bytes(n))
}
