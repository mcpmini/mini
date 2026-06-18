//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireSpawnLock takes an exclusive advisory lock on <configDir>/daemon.lock so that,
// when many proxies discover a dead daemon at once, exactly one of them spawns the
// replacement and the rest block here until it is up. It is purely a herd-collapse
// optimization: the OS socket bind in startDaemonHTTP is the real single-winner guarantee,
// so losing or skipping this lock only wastes spawns, never breaks correctness.
func acquireSpawnLock(configDir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(configDir, "daemon.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	// The kernel releases flock when the holder exits for any reason, including SIGKILL, so
	// a spawner dying mid-start can't leave a stale lock — no PID/timestamp needed in the file.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close() //nolint:errcheck
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()                                   //nolint:errcheck
	}, nil
}
