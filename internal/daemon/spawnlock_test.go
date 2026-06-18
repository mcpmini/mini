//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireSpawnLock_createsLockFile0600(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireSpawnLock(dir)
	if err != nil {
		t.Fatalf("acquireSpawnLock: %v", err)
	}
	defer release()
	info, err := os.Stat(filepath.Join(dir, "daemon.lock"))
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("lock file perm = %#o, want 0600", info.Mode().Perm())
	}
}

// Two separate os.OpenFile calls get distinct file descriptions, so flock(LOCK_EX) contends even within one process.
func TestAcquireSpawnLock_isExclusive(t *testing.T) {
	dir := t.TempDir()
	release1, err := acquireSpawnLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	secondAcquired := make(chan struct{})
	go func() {
		release2, err := acquireSpawnLock(dir)
		if err != nil {
			t.Errorf("second acquire: %v", err)
			close(secondAcquired)
			return
		}
		close(secondAcquired)
		release2()
	}()

	select {
	case <-secondAcquired:
		t.Fatal("second acquire returned while first lock was held — not exclusive")
	case <-time.After(200 * time.Millisecond):
	}

	release1()

	select {
	case <-secondAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire did not unblock after first release — lock leaked")
	}
}
