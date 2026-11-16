//go:build !windows

package daemon

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDetachFromTerminal_startsTheProcessInItsOwnSession(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	detachFromTerminal(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck // the process may already have exited; Wait reaps it either way
		cmd.Wait()         //nolint:errcheck // a killed process always reports an error
	})

	session, err := unix.Getsid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	ours, err := unix.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	if session != cmd.Process.Pid || session == ours {
		t.Errorf("session = %d, want the process's own (%d), not the test's (%d)", session, cmd.Process.Pid, ours)
	}
}
