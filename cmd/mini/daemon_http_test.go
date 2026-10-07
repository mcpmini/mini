package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type daemonHTTPTestService struct {
	started  chan struct{}
	canceled chan struct{}
	release  <-chan struct{}
	finished chan struct{}
}

func (s *daemonHTTPTestService) ReleaseStartupHolds() {}

func (s *daemonHTTPTestService) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (s *daemonHTTPTestService) RunSessionEviction(ctx context.Context, _ time.Duration) {
	close(s.started)
	<-ctx.Done()
	if s.canceled != nil {
		close(s.canceled)
	}
	if s.release != nil {
		<-s.release
	}
	close(s.finished)
}

type daemonHTTPTestListener struct {
	acceptStarted chan struct{}
	closed        chan struct{}
	acceptErr     error
	once          sync.Once
}

func newDaemonHTTPTestListener(acceptErr error) *daemonHTTPTestListener {
	return &daemonHTTPTestListener{
		acceptStarted: make(chan struct{}),
		closed:        make(chan struct{}),
		acceptErr:     acceptErr,
	}
}

func (l *daemonHTTPTestListener) Accept() (net.Conn, error) {
	close(l.acceptStarted)
	if l.acceptErr != nil {
		return nil, l.acceptErr
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *daemonHTTPTestListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *daemonHTTPTestListener) Addr() net.Addr { return daemonHTTPTestAddr("daemon") }

type daemonHTTPTestAddr string

func (a daemonHTTPTestAddr) Network() string { return "daemon-test" }
func (a daemonHTTPTestAddr) String() string  { return string(a) }

func TestDaemonHTTPServeFailureReturnsAndJoinsEviction(t *testing.T) {
	synctest.Test(t, testDaemonHTTPServeFailureReturnsAndJoinsEviction)
}

func testDaemonHTTPServeFailureReturnsAndJoinsEviction(t *testing.T) {
	serveErr := errors.New("permanent accept failure")
	listener := newDaemonHTTPTestListener(serveErr)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseWorker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := &daemonHTTPTestService{
		started: make(chan struct{}), canceled: make(chan struct{}),
		release: release, finished: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		result <- startDaemonHTTP(ctx, DaemonHTTPParams{Srv: service, Listener: listener})
	}()
	awaitDaemonHTTP(t, service.started)
	awaitDaemonHTTP(t, listener.closed)
	awaitDaemonHTTP(t, service.canceled)
	synctest.Wait()
	select {
	case err := <-result:
		t.Fatalf("startDaemonHTTP returned before eviction finished: %v", err)
	default:
	}
	releaseWorker()
	if err := awaitDaemonHTTPError(t, result); !errors.Is(err, serveErr) {
		t.Fatalf("startDaemonHTTP error = %v, want cause %v", err, serveErr)
	}
	awaitDaemonHTTP(t, service.finished)
}

func TestDaemonHTTPCancelShutsDownAndJoinsWorkers(t *testing.T) {
	listener := newDaemonHTTPTestListener(nil)
	service := &daemonHTTPTestService{
		started: make(chan struct{}), finished: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- startDaemonHTTP(ctx, DaemonHTTPParams{Srv: service, Listener: listener})
	}()
	awaitDaemonHTTP(t, service.started)
	awaitDaemonHTTP(t, listener.acceptStarted)
	cancel()
	if err := awaitDaemonHTTPError(t, result); err != nil {
		t.Fatalf("startDaemonHTTP after cancellation: %v", err)
	}
	awaitDaemonHTTP(t, listener.closed)
	awaitDaemonHTTP(t, service.finished)
}

func awaitDaemonHTTP(t *testing.T, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for daemon HTTP lifecycle event")
	}
}

func awaitDaemonHTTPError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for daemon HTTP lifecycle result")
		return nil
	}
}
