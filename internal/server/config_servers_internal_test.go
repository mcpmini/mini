//go:build test

package server

import (
	"context"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestApplyServerFile_skipsANameAnotherOperationHolds(t *testing.T) {
	srv := newInstallTestServer(t)
	if err := srv.AddConnection(
		t.Context(),
		config.ServerConfig{Name: "svc"},
		&transport.FakeConnection{},
	); err != nil {
		t.Fatal(err)
	}
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	unlock := srv.serverNames.lock("svc")

	removedWhileHeld := srv.applyServerFile(t.Context(), "svc")
	unlock()

	if removedWhileHeld {
		t.Error("applyServerFile removed svc while another operation held its name")
	}
	if !srv.applyServerFile(t.Context(), "svc") {
		t.Error("applyServerFile kept svc once its name was free, want the next pass to remove it")
	}
}

func TestClose_stopsTheConfigReloadLoop(t *testing.T) {
	srv := newInstallTestServer(t)
	fc := clock.NewFake()
	srv.clock = fc
	srv.StartConfigReload(context.Background())
	if err := fc.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal("the reload loop never started its ticker:", err)
	}

	srv.Close()

	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	if fc.BlockUntilContext(stopped, 1) == nil {
		t.Error("the reload loop's ticker is still running after Close")
	}
}

func TestNameLocks_aFailedTryLockLeavesNoReference(t *testing.T) {
	var locks nameLocks
	unlock := locks.lock("svc")

	if _, ok := locks.tryLock("svc"); ok {
		t.Fatal("tryLock took a name another caller holds")
	}
	unlock()

	if len(locks.locks) != 0 {
		t.Errorf("locks = %v after every caller left, want none", locks.locks)
	}
	unlock, ok := locks.tryLock("svc")
	if !ok {
		t.Fatal("tryLock failed on a free name")
	}
	unlock()
}

func TestBackgroundLoop_stopCancelsTheLoopAndWaitsForIt(t *testing.T) {
	var loop backgroundLoop
	exited := false
	loop.start(context.Background(), func(ctx context.Context) {
		<-ctx.Done()
		exited = true
	})

	stopped := make(chan struct{})
	go func() {
		loop.stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop didn't end a loop that runs until its context is canceled")
	}
	if !exited {
		t.Error("stop returned before the loop exited")
	}
}

func TestBackgroundLoop_startAfterStopDoesNotRun(t *testing.T) {
	var loop backgroundLoop
	loop.stop()

	ran := false
	loop.start(context.Background(), func(context.Context) { ran = true })
	loop.stop()

	if ran {
		t.Error("a loop started after stop ran, so it would outlive Close")
	}
}
