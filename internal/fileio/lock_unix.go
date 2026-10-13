//go:build !windows

package fileio

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return ErrLockHeld
		}
		return err
	}
}

func unlockAndClose(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing the descriptor releases the lock anyway
	f.Close()                                   //nolint:errcheck // nothing was written through this descriptor
}
