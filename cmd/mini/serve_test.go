package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
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
