//go:build test

package auth_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/testutil"
)

func holdTokenLock(t *testing.T, lock auth.TokenLock) (release func()) {
	t.Helper()
	held := make(chan struct{})
	releaseCh := make(chan struct{})
	go func() {
		err := lock.Do(context.Background(), func(auth.TokenFile) error {
			close(held)
			<-releaseCh
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}()
	<-held
	return func() { close(releaseCh) }
}

func TestTokenLock_saveWaitsOutAHolderThenWrites(t *testing.T) {
	fake := clock.NewFake()
	lock := auth.TokenLock{ConfigDir: t.TempDir(), ServerName: "srv", Clock: fake}
	release := holdTokenLock(t, lock)
	saved := make(chan error, 1)
	go func() { saved <- lock.Save(context.Background(), &oauth2.Token{AccessToken: "login"}) }()

	testutil.WaitForTimers(t, fake, 1)
	release()
	fake.Advance(time.Second)
	if err := <-saved; err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := auth.Load(lock.ConfigDir, "srv")
	if err != nil || got.AccessToken != "login" {
		t.Errorf("token on disk = %v, %v; want the saved login", got, err)
	}
}

func TestTokenLock_waitEndingSkipsTheWork(t *testing.T) {
	fake := clock.NewFake()
	lock := auth.TokenLock{ConfigDir: t.TempDir(), ServerName: "srv", Clock: fake}
	release := holdTokenLock(t, lock)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	ran := false
	done := make(chan error, 1)
	go func() { done <- lock.Do(ctx, func(auth.TokenFile) error { ran = true; return nil }) }()

	testutil.WaitForTimers(t, fake, 1)
	cancel()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "another mini process") {
		t.Errorf("Do = %v, want the wait error", err)
	}
	if ran {
		t.Error("the work ran without the lock after the wait ended")
	}
}

func TestTokenLock_unusableLockFileStillRunsTheWork(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "srv.token.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := auth.TokenLock{ConfigDir: dir, ServerName: "srv", Clock: clock.NewFake()}

	err := lock.Save(context.Background(), &oauth2.Token{AccessToken: "login"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got, err := auth.Load(dir, "srv"); err != nil || got.AccessToken != "login" {
		t.Errorf("token on disk = %v, %v; want the saved login", got, err)
	}
}
