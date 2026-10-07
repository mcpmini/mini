package main

import (
	"fmt"
	"io"
)

func printNotice(out io.Writer, format string, args ...any) {
	//nolint:errcheck // Notice delivery does not change saved configuration or returned operation errors.
	_, _ = fmt.Fprintf(out, format, args...)
}
