package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/ops"
)

func newCleanupCmd(configDir string) *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup",
		Short: "Delete expired response files",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCleanup(configDir, cmd.OutOrStdout(), clock.System())
		},
	}
}

func runCleanup(configDir string, out io.Writer, clock clock.Clock) error {
	removed, freed, err := ops.PurgeExpiredResponses(configDir, clock.Now())
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
