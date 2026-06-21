//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquireSpawnLock serializes daemon spawning so that concurrent proxies produce exactly one
// daemon instead of a herd. The socket bind is the correctness guarantee (only one binder wins);
// this lock eliminates wasted spawn attempts and the TOCTOU window in bindSocket during slow
// startup (OAuth injection, upstream connections).
func acquireSpawnLock(configDir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(configDir, "daemon.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()                                    //nolint:errcheck
	}, nil
}
