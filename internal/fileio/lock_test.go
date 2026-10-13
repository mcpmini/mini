//go:build test

package fileio_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/fileio"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestLockFile_secondHolderWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	fake := clock.NewFake()
	unlockFirst, err := fileio.LockFile(context.Background(), fake, path)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	go func() {
		unlock, err := fileio.LockFile(context.Background(), fake, path)
		if err != nil {
			t.Error(err)
			return
		}
		acquired <- unlock
	}()

	testutil.WaitForTimers(t, fake, 1)
	select {
	case <-acquired:
		t.Fatal("second holder got the lock while the first still held it")
	default:
	}
	unlockFirst()
	fake.Advance(time.Second)
	select {
	case unlock := <-acquired:
		unlock()
	case <-t.Context().Done():
		t.Fatal("second holder never got the released lock")
	}
}

func TestLockFile_waitEndsWithContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	fake := clock.NewFake()
	unlock, err := fileio.LockFile(context.Background(), fake, path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fileio.LockFile(ctx, fake, path)
		done <- err
	}()

	testutil.WaitForTimers(t, fake, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("LockFile = %v, want context.Canceled", err)
	}
}

func TestLockFile_unopenablePathIsNotReportedAsHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "x.lock")

	_, err := fileio.LockFile(context.Background(), clock.NewFake(), path)

	if err == nil || errors.Is(err, fileio.ErrLockHeld) {
		t.Errorf("LockFile = %v, want an open error", err)
	}
}
