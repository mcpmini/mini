//go:build !windows

package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// stopDaemon ends the daemon a scenario's `mini add` or `mini auth` started for configDir: it
// detaches from this process, so it would otherwise outlive the scenario and its config dir.
func stopDaemon(configDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pgrep", "-f", configDir+" daemon").Output()
	if err != nil {
		return // pgrep exits 1 when no daemon was started
	}
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil {
			syscall.Kill(pid, syscall.SIGTERM) //nolint:errcheck // the daemon may already be gone
		}
	}
}
