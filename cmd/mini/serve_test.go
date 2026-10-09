package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

type blockReader struct {
	block  chan struct{}
	closed chan struct{}
}

func newBlockReader() *blockReader {
	return &blockReader{
		block:  make(chan struct{}),
		closed: make(chan struct{}),
	}
}

func (r *blockReader) Read(p []byte) (int, error) {
	select {
	case <-r.closed:
		return 0, io.ErrClosedPipe
	case <-r.block:
		return 0, io.EOF
	}
}

func (r *blockReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func TestServeUntilCanceled_drainWaitsForServe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newBlockReader()
	drainGate := make(chan struct{})
	draining := make(chan struct{})
	fakeServe := func(ctx context.Context, in io.Reader, out io.Writer) error {
		<-ctx.Done()
		close(draining)
		<-drainGate
		return nil
	}
	result := make(chan error, 1)
	go func() {
		result <- serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: fakeServe, In: r, Out: io.Discard})
	}()
	cancel()
	select {
	case <-draining:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not reach drain phase within 2s")
	}
	select {
	case <-result:
		t.Fatal("serveUntilCanceled returned before drain gate was released")
	default:
	}
	close(drainGate)
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveUntilCanceled did not return within 2s after drain gate released")
	}
}

func TestServeUntilCanceled_closesReaderAndReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newBlockReader()
	fakeServe := func(ctx context.Context, in io.Reader, out io.Writer) error {
		_, err := io.ReadAll(in)
		return err
	}
	go cancel()
	result := make(chan error, 1)
	go func() {
		result <- serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: fakeServe, In: r, Out: io.Discard})
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveUntilCanceled did not return within 5s after context cancel")
	}
	select {
	case <-r.closed:
	default:
		t.Error("expected reader to be closed after context cancellation")
	}
}

func TestServeUntilCanceled_serveCompletesOnEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newBlockReader()
	fakeServe := func(ctx context.Context, in io.Reader, out io.Writer) error {
		io.Copy(io.Discard, in) //nolint:errcheck
		return nil
	}
	close(r.block)
	result := make(chan error, 1)
	go func() {
		result <- serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: fakeServe, In: r, Out: io.Discard})
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("expected nil on normal EOF, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveUntilCanceled did not return within 5s on EOF")
	}
}

func TestServeUntilCanceled_unrelatedReadErrorPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newBlockReader()
	want := errors.New("scan error")
	fakeServe := func(ctx context.Context, in io.Reader, out io.Writer) error {
		return want
	}
	err := serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: fakeServe, In: r, Out: io.Discard})
	if !errors.Is(err, want) {
		t.Errorf("expected %v, got %v", want, err)
	}
}

func TestServeUntilCanceled_outputFailureClosesPipeAndJoinsServe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	want := errors.New("stdout failed")
	srv := server.New(server.Params{
		Config: &config.Config{}, ConfigDir: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	defer srv.Close()
	result := make(chan error, 1)
	go func() {
		result <- serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: srv.Serve, In: pr, Out: failingServeWriter{want}})
	}()
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n"
	_, writeErr := io.WriteString(pw, initialize)
	if writeErr != nil {
		t.Fatalf("write initialize request: %v", writeErr)
	}
	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatalf("serveUntilCanceled error = %v, want failure cause %v", err, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveUntilCanceled did not close the input pipe and join Serve")
	}
	if ctx.Err() != nil {
		t.Fatalf("parent context canceled: %v", ctx.Err())
	}
}

type failingServeWriter struct{ err error }

func (w failingServeWriter) Write([]byte) (int, error) { return 0, w.err }

var errStdinSentinel = errors.New("stdin read error")

type fixedErrReader struct{ err error }

func (r *fixedErrReader) Read(p []byte) (int, error) { return 0, r.err }

func TestStdinPipe_dataAndEOF(t *testing.T) {
	src := strings.NewReader("hello")
	r := stdinPipe(src)
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestStdinPipe_sourceError(t *testing.T) {
	src := &fixedErrReader{err: errStdinSentinel}
	r := stdinPipe(src)
	defer r.Close()
	_, err := io.ReadAll(r)
	if !errors.Is(err, errStdinSentinel) {
		t.Errorf("expected %v, got %v", errStdinSentinel, err)
	}
}

func TestShutdownContext_firstSignal_releasesSignalHandling(t *testing.T) {
	var deliverSignal context.CancelFunc
	released := make(chan struct{})
	var once sync.Once
	var registered []os.Signal
	fakeNotify := func(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
		registered = sigs
		ctx, cancel := context.WithCancel(parent)
		deliverSignal = cancel
		return ctx, func() { once.Do(func() { close(released) }); cancel() }
	}
	ctx, _ := shutdownContext(fakeNotify)
	if !slices.Equal(registered, []os.Signal{syscall.SIGINT, syscall.SIGTERM}) {
		t.Errorf("registered signals = %v, want SIGINT and SIGTERM", registered)
	}
	select {
	case <-released:
		t.Fatal("signal handling released before any signal")
	default:
	}
	if ctx.Err() != nil {
		t.Fatal("serve context cancelled before any signal")
	}
	deliverSignal()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("first signal did not release signal handling, so a second signal would be swallowed")
	}
	if ctx.Err() == nil {
		t.Error("first signal must cancel the serve context")
	}
}
