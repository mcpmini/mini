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

	"github.com/mcpmini/mini/internal/testutil"
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
	result := testutil.RunInBackground(func() error {
		return startDaemonHTTP(ctx, DaemonHTTPParams{Srv: service, Listener: listener})
	})
	testutil.WaitForReceive(t, service.started, "session eviction to start")
	testutil.WaitForReceive(t, listener.closed, "the failed listener to close")
	testutil.WaitForReceive(t, service.canceled, "session eviction to be canceled")
	synctest.Wait()
	select {
	case err := <-result:
		t.Fatalf("startDaemonHTTP returned before eviction finished: %v", err)
	default:
	}
	releaseWorker()
	if err := testutil.WaitForReceive(t, result, "startDaemonHTTP to return"); !errors.Is(err, serveErr) {
		t.Fatalf("startDaemonHTTP error = %v, want cause %v", err, serveErr)
	}
	testutil.WaitForReceive(t, service.finished, "session eviction to finish")
}

func TestDaemonHTTPCancelShutsDownAndJoinsWorkers(t *testing.T) {
	listener := newDaemonHTTPTestListener(nil)
	service := &daemonHTTPTestService{
		started: make(chan struct{}), finished: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := testutil.RunInBackground(func() error {
		return startDaemonHTTP(ctx, DaemonHTTPParams{Srv: service, Listener: listener})
	})
	testutil.WaitForReceive(t, service.started, "session eviction to start")
	testutil.WaitForReceive(t, listener.acceptStarted, "the listener to accept")
	cancel()
	if err := testutil.WaitForReceive(t, result, "startDaemonHTTP to return"); err != nil {
		t.Fatalf("startDaemonHTTP after cancellation: %v", err)
	}
	testutil.WaitForReceive(t, listener.closed, "the listener to close")
	testutil.WaitForReceive(t, service.finished, "session eviction to finish")
}
