package randutil

import (
	"crypto/rand"
	"encoding/hex"
)

func Bytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// HexString returns n random bytes encoded as a lowercase hex string (length 2n).
func HexString(n int) string {
	return hex.EncodeToString(Bytes(n))
}
