// Package randutil provides helpers for cryptographically secure random bytes.
//
// crypto/rand.Read never returns a non-nil error. If the OS random source is
// unavailable it crashes the program irrecoverably rather than returning an
// error. See https://github.com/golang/go/blob/go1.24.0/src/crypto/rand/rand.go#L60-L83
package randutil

import (
	"crypto/rand"
	"encoding/hex"
)

// Bytes returns n cryptographically secure random bytes.
func Bytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b) //nolint:errcheck
	return b
}

// HexString returns n random bytes encoded as a lowercase hex string (length 2n).
func HexString(n int) string {
	return hex.EncodeToString(Bytes(n))
}
