package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/mcpmini/mini/internal/transport"
)

// Serve dispatches each request in its own goroutine so slow upstream calls
// do not block subsequent requests from the same agent.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sessionID := transport.NewSessionID()
	session := s.sessions.getOrCreate(sessionID)
	defer s.sessions.delete(sessionID)
	return s.serveLoop(ctx, serveLoopParams{Input: in, Output: out, Session: session})
}

type serveLoopParams struct {
	Input   io.Reader
	Output  io.Writer
	Session *Session
}

func (s *Server) serveLoop(ctx context.Context, p serveLoopParams) error {
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := newSerializedWriter(p.Output, cancel)
	writer.observeResponseFailure(p.Output)
	stopNotifications := startSessionNotifyForwarder(p.Session, writer.Write)
	scanErr := s.scanServeInput(serveCtx, serveInputParams{Input: p.Input, Session: p.Session, Writer: writer})
	stopNotifications()
	return errors.Join(writer.Err(), scanErr)
}

type serveInputParams struct {
	Input   io.Reader
	Session *Session
	Writer  *serializedWriter
}

func (s *Server) scanServeInput(ctx context.Context, p serveInputParams) error {
	var wg sync.WaitGroup
	scanner := transport.NewScanner(p.Input)
	for scanner.Scan() {
		if p.Writer.Err() != nil || ctx.Err() != nil {
			break
		}
		s.handleScannedLine(
			handleScannedLineParams{
				ctx: ctx, rawLine: scanner.Bytes(), session: p.Session,
				writeOut: p.Writer.Write, wg: &wg,
			},
		)
	}
	p.Session.markAborted()
	wg.Wait()
	return scanner.Err()
}

func (s *Server) handleLineCancellable(ctx context.Context, line []byte, session *Session) (transport.Response, bool) {
	rawID := peekRequestID(line)
	if len(rawID) == 0 {
		return s.handleLine(ctx, line, session)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	session.registerInFlight(rawID, cancel)
	defer session.removeInFlight(rawID)
	return s.handleLine(ctx, line, session)
}

type handleLineAndReplyParams struct {
	Line     []byte
	Session  *Session
	WriteOut func(any)
}

func (s *Server) handleLineAndReply(ctx context.Context, p handleLineAndReplyParams) {
	if resp, send := s.handleLineCancellable(ctx, p.Line, p.Session); send {
		p.WriteOut(resp)
	}
}

func peekMethod(line []byte) string {
	var peek struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(line, &peek); err != nil {
		return ""
	}
	return peek.Method
}

// peekRequestID extracts the raw JSON "id" field from a JSON-RPC line without
// fully parsing it. Returns nil for notifications (no id) or parse failures.
func peekRequestID(line []byte) json.RawMessage {
	var peek struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(line, &peek); err != nil {
		return nil
	}
	if len(peek.ID) == 0 || string(peek.ID) == "null" {
		return nil
	}
	return peek.ID
}

func (s *Server) handleLine(ctx context.Context, line []byte, session *Session) (transport.Response, bool) {
	session.touch()
	var req transport.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, transport.CodeParseError, "parse error"), true
	}
	if req.JsonRPC != transport.JsonRPCVersion {
		return errorResponse(req.ID, transport.CodeInvalidRequest, "jsonrpc must be \"2.0\""), true
	}
	if req.ID == nil {
		s.routeRequest(ctx, req, session) //nolint:errcheck // Notifications have no response channel for errors.
		return transport.Response{}, false
	}
	return s.handleRequest(ctx, req, session)
}

type handleScannedLineParams struct {
	ctx      context.Context
	rawLine  []byte
	session  *Session
	writeOut func(any)
	wg       *sync.WaitGroup
}

func (s *Server) handleScannedLine(p handleScannedLineParams) {
	if len(p.rawLine) == 0 {
		return
	}
	line := bytes.Clone(p.rawLine)
	if s.handleListenLine(p, line) {
		return
	}
	method := peekMethod(line)
	// Lifecycle and cancellation transitions must finish before later requests start.
	if method == "initialize" || method == transport.NotificationInitialized ||
		method == transport.NotificationCancelled {
		if resp, send := s.handleLine(p.ctx, line, p.session); send {
			p.writeOut(resp)
		}
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		s.handleLineAndReply(p.ctx, handleLineAndReplyParams{
			Line: line, Session: p.session, WriteOut: p.writeOut,
		})
	}()
}

type serializedWriter struct {
	mu        sync.Mutex
	out       io.Writer
	err       error
	cancel    func()
	onFailure func(error)
}

func newSerializedWriter(out io.Writer, cancel func()) *serializedWriter {
	return &serializedWriter{out: out, cancel: cancel}
}

func (w *serializedWriter) observeResponseFailure(out io.Writer) {
	if failed, ok := out.(interface{ ResponseFailed(error) }); ok {
		w.onFailure = failed.ResponseFailed
	}
}

func (w *serializedWriter) Write(v any) {
	w.mu.Lock()
	if w.err != nil {
		w.mu.Unlock()
		return
	}
	err := writeJSON(w.out, v)
	if err != nil {
		w.err = err
	}
	w.mu.Unlock()
	if err != nil {
		if w.onFailure != nil {
			w.onFailure(err)
		}
		w.cancel()
	}
}

func (w *serializedWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	n, err := fmt.Fprintf(w, "%s\n", b)
	if err != nil {
		return err
	}
	if n != len(b)+1 {
		return io.ErrShortWrite
	}
	return nil
}
