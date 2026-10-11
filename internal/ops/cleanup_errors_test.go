package ops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestPurgeEntry(t *testing.T) {
	t.Run("counts nonempty file removal", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "response.json")
		testutil.WriteFile(t, path, "response")

		freed, removed, err := purgeEntry(path, int64(len("response")))
		if err != nil || !removed || freed != int64(len("response")) {
			t.Fatalf("purgeEntry = (%d, %t, %v), want (%d, true, nil)", freed, removed, err, len("response"))
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("response stat error = %v, want not-exist", err)
		}
	})

	t.Run("counts zero-byte file removal", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.json")
		testutil.WriteFile(t, path, "")

		freed, removed, err := purgeEntry(path, 0)
		if err != nil || !removed || freed != 0 {
			t.Fatalf("purgeEntry = (%d, %t, %v), want (0, true, nil)", freed, removed, err)
		}
	})

	t.Run("reports nonempty directory removal failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "blocked-response")
		testutil.WriteFile(t, filepath.Join(path, "child"), "private contents")

		freed, removed, err := purgeEntry(path, 12)
		if err == nil || removed || freed != 0 {
			t.Fatalf("purgeEntry = (%d, %t, %v), want (0, false, error)", freed, removed, err)
		}
		assertDeleteCause(t, err)
		if !strings.Contains(err.Error(), filepath.Base(path)) || strings.Contains(err.Error(), "private contents") {
			t.Fatalf("error %q lacks safe response context or exposes file contents", err)
		}
	})

	t.Run("treats vanished path as benign", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "vanished.json")

		freed, removed, err := purgeEntry(path, 12)
		if err != nil || removed || freed != 0 {
			t.Fatalf("purgeEntry = (%d, %t, %v), want (0, false, nil)", freed, removed, err)
		}
	})
}

func TestPurgeExpiredContinuesAfterDeleteFailure(t *testing.T) {
	dir := t.TempDir()
	successPath := filepath.Join(dir, "z-success.json")
	blockedPath := filepath.Join(dir, "a-blocked.json")
	testutil.WriteFile(t, successPath, "success")
	testutil.WriteFile(t, blockedPath, "original")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if removeErr := os.Remove(blockedPath); removeErr != nil {
		t.Fatal(removeErr)
	}
	if mkdirErr := os.Mkdir(blockedPath, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	testutil.WriteFile(t, filepath.Join(blockedPath, "child"), "private contents")

	removed, freed, err := purgeExpired(dir, entries, time.Now().Add(time.Hour))
	if removed != 1 || freed != int64(len("success")) {
		t.Errorf("purgeExpired counts = (%d, %d), want (1, %d)", removed, freed, len("success"))
	}
	if err == nil {
		t.Fatal("purgeExpired error = nil, want blocked removal error")
	}
	assertDeleteCause(t, err)
	if _, err := os.Stat(successPath); !os.IsNotExist(err) {
		t.Errorf("successful response stat error = %v, want not-exist", err)
	}
	if info, err := os.Stat(blockedPath); err != nil || !info.IsDir() {
		t.Errorf("blocked response stat = (%v, %v), want existing directory", info, err)
	}
}

func assertDeleteCause(t *testing.T, err error) {
	t.Helper()
	var cause syscall.Errno
	if !errors.As(err, &cause) || !errors.Is(err, cause) {
		t.Fatalf("error %v does not preserve its removal cause", err)
	}
}
