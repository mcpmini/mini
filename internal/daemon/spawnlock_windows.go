//go:build windows

package daemon

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// acquireSpawnLock serializes daemon spawning so that concurrent proxies produce exactly one
// daemon instead of a herd. See spawnlock_unix.go for rationale.
//
// Modeled after gofrs/flock's windows implementation:
// https://github.com/gofrs/flock/blob/c08bb665ea1975bfcc3182d0033ed1ee7c9e735a/flock_windows.go#L50-L73
func acquireSpawnLock(configDir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(configDir, "daemon.lock"), os.O_RDWR|os.O_CREATE, 0600)
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
