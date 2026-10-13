//go:build windows

package fileio

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLock(f *os.File) error {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	// LockFileEx needs a non-nil Overlapped even for synchronous use; one byte suffices for an advisory lock.
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrLockHeld
	}
	return err
}

func unlockAndClose(f *os.File) {
	handle := windows.Handle(f.Fd())
	windows.UnlockFileEx(handle, 0, 1, 0, new(windows.Overlapped)) //nolint:errcheck // closing releases the lock anyway
	f.Close()                                                      //nolint:errcheck // nothing was written through this handle
}
