//go:build windows

package daemon

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// acquireSpawnLock serializes daemon spawning so that concurrent proxies produce exactly one
// daemon instead of a herd. See spawnlock_unix.go for rationale.
// LockFileEx requires a non-nil Overlapped even for synchronous use; locking 1 byte suffices as an advisory mutex.
func acquireSpawnLock(configDir string) (release func(), err error) {
	lockPath := filepath.Join(configDir, "internal", "daemon.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol) //nolint:errcheck
		f.Close()                                                  //nolint:errcheck
	}, nil
}
