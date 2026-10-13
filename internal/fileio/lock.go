package fileio

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

// ErrLockHeld means another open file description holds the lock.
var ErrLockHeld = errors.New("lock held by another process")

const lockPollInterval = 50 * time.Millisecond

// LockFile locks path exclusively, waiting while another holder has it until ctx ends. The lock is
// advisory and belongs to the open file, so two opens in one process exclude each other too. Errors
// other than ctx's mean the lock can't be taken at all (e.g. a network mount without file locks).
func LockFile(ctx context.Context, c clock.Clock, path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := tryLock(f)
		if err == nil {
			return func() { unlockAndClose(f) }, nil
		}
		if !errors.Is(err, ErrLockHeld) {
			f.Close() //nolint:errcheck // the lock error is returned; Close only releases the descriptor
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close() //nolint:errcheck // the context error is returned; Close only releases the descriptor
			return nil, ctx.Err()
		case <-c.After(lockPollInterval):
		}
	}
}
