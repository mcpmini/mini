package main

import (
	"context"
	"errors"
	"io"
	"testing"
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

func TestServeUntilCanceled_closesReaderAndReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newBlockReader()
	fakeServe := func(ctx context.Context, in io.Reader, out io.Writer) error {
		_, err := io.ReadAll(in)
		return err
	}
	go cancel()
	err := serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: fakeServe, In: r, Out: io.Discard})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	select {
	case <-r.closed:
	default:
		t.Error("expected reader to be closed after context cancellation")
	}
}

func TestServeUntilCanceled_normalEOFStopsWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newBlockReader()
	fakeServe := func(ctx context.Context, in io.Reader, out io.Writer) error {
		return nil
	}
	err := serveUntilCanceled(serveWatchParams{Ctx: ctx, Serve: fakeServe, In: r, Out: io.Discard})
	if err != nil {
		t.Errorf("expected nil on normal EOF, got %v", err)
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
