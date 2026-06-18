//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireSpawnLock collapses the respawn herd: when many proxies detect a dead daemon at
// once, exactly one spawns the replacement while the rest block here until it is up.
// Herd-collapse optimization only — the OS socket bind is the real single-winner guarantee;
// losing or skipping this lock only wastes spawn attempts, never breaks correctness.
func acquireSpawnLock(configDir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(configDir, "daemon.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	// Kernel auto-releases flock on process exit (including SIGKILL) — stale locks are impossible.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close() //nolint:errcheck
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()                                   //nolint:errcheck
	}, nil
}
