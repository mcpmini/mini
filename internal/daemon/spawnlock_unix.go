//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireSpawnLock takes an exclusive advisory lock on <configDir>/daemon.lock so that,
// when many proxies discover a dead daemon at once, exactly one of them spawns the
// replacement and the rest block here until it is up. The lock is purely a herd-collapse
// optimization: the OS socket bind in startDaemonHTTP is the ultimate single-winner
// guarantee, so losing or skipping this lock only wastes spawns, never breaks correctness.
//
// flock is released automatically by the kernel when the holding process exits for any
// reason (including SIGKILL), so a spawner that dies mid-start cannot leave a stale lock
// that deadlocks the others — there is deliberately no PID or timestamp in the lock file.
func acquireSpawnLock(configDir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(configDir, "daemon.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close() //nolint:errcheck
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()                                   //nolint:errcheck
	}, nil
}
