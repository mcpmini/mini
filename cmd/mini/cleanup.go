package main

import (
	"fmt"
	"io"
	"time"

	"github.com/mcpmini/mini/internal/ops"
)

func runCleanup(configDir string, out io.Writer) error {
	removed, freed, err := ops.PurgeExpiredResponses(configDir, time.Now()) //nolint:clocklint
	if err != nil {
		return fmt.Errorf("cleanup: %w", err)
	}
	if removed == 0 {
		fmt.Fprintln(out, "nothing to clean up")
	} else {
		fmt.Fprintf(out, "removed %d file(s), freed %.1f MB\n", removed, float64(freed)/1e6)
	}
	return nil
}
