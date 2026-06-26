package main

import (
	"fmt"
	"io"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/ops"
)

func runCleanup(configDir string, out io.Writer, appClock clock.Clock) error {
	removed, freed, err := ops.PurgeExpiredResponses(configDir, appClock.Now())
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
