package transport

import (
	"bufio"
	"io"
)

// NewScanner creates a bufio.Scanner sized for large MCP JSON responses (up to 16MB).
func NewScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return s
}
