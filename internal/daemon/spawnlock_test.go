//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireSpawnLock(t *testing.T) {
	t.Run("creates 0600 lock file", func(t *testing.T) {
		dir := t.TempDir()
		release, err := acquireSpawnLock(dir)
		if err != nil {
			t.Fatalf("acquireSpawnLock: %v", err)
		}
		defer release()
		info, err := os.Stat(filepath.Join(dir, "internal", "daemon.lock"))
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("perm = %#o, want 0600", info.Mode().Perm())
		}
	})

	t.Run("exclusive", func(t *testing.T) {
		dir := t.TempDir()
		release1, err := acquireSpawnLock(dir)
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}

		acquired := make(chan error, 1)
		go func() {
			release2, err := acquireSpawnLock(dir)
			if err != nil {
				acquired <- err
				return
			}
			acquired <- nil
			release2()
		}()

		select {
		case err := <-acquired:
			if err != nil {
				t.Fatalf("second acquire errored: %v", err)
			}
			t.Fatal("second acquire returned while first lock held")
		case <-time.After(200 * time.Millisecond):
		}

		release1()

		select {
		case err := <-acquired:
			if err != nil {
				t.Fatalf("second acquire errored after release: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("second acquire did not unblock after release")
		}
	})
}
