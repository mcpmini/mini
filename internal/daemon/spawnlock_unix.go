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
	lockPath := filepath.Join(configDir, "internal", "daemon", "daemon.lock")
	if mkdirErr := os.MkdirAll(filepath.Dir(lockPath), 0o700); mkdirErr != nil {
		return nil, mkdirErr
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		//nolint:errcheck // the lock acquisition error is returned; Close only releases the opened file.
		f.Close()
		return nil, err
	}
	return func() {
		//nolint:errcheck // socket bind remains the spawn correctness gate if unlock fails.
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		//nolint:errcheck // closing the descriptor releases its OS lock; socket bind remains the correctness gate.
		f.Close()
	}, nil
}
