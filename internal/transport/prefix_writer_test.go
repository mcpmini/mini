package transport

import (
	"context"
	"log/slog"
	"testing"
)

type logSink struct{ msgs []string }

func (s *logSink) Enabled(_ context.Context, _ slog.Level) bool  { return true }
func (s *logSink) Handle(_ context.Context, r slog.Record) error { s.msgs = append(s.msgs, r.Message); return nil }
func (s *logSink) WithAttrs(_ []slog.Attr) slog.Handler          { return s }
func (s *logSink) WithGroup(_ string) slog.Handler               { return s }

func TestPrefixWriter_singleCompleteLine(t *testing.T) {
	sink := &logSink{}
	w := &prefixWriter{logger: slog.New(sink), prefix: "[srv] "}

	n, err := w.Write([]byte("hello\n"))
	if err != nil || n != 6 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if len(sink.msgs) != 1 || sink.msgs[0] != "[srv] hello" {
		t.Errorf("unexpected messages: %v", sink.msgs)
	}
}

func TestPrefixWriter_partialLine_flushesOnSecondWrite(t *testing.T) {
	sink := &logSink{}
	w := &prefixWriter{logger: slog.New(sink), prefix: ""}

	w.Write([]byte("partial"))
	if len(sink.msgs) != 0 {
		t.Error("expected no output before newline")
	}
	w.Write([]byte(" line\n"))
	if len(sink.msgs) != 1 || sink.msgs[0] != "partial line" {
		t.Errorf("unexpected: %v", sink.msgs)
	}
}

func TestPrefixWriter_multipleNewlinesInOneWrite(t *testing.T) {
	sink := &logSink{}
	w := &prefixWriter{logger: slog.New(sink), prefix: ""}
	w.Write([]byte("a\nb\nc\n"))
	if len(sink.msgs) != 3 {
		t.Errorf("expected 3 messages, got %d: %v", len(sink.msgs), sink.msgs)
	}
}

func TestPrefixWriter_noTrailingNewline_buffered(t *testing.T) {
	sink := &logSink{}
	w := &prefixWriter{logger: slog.New(sink), prefix: ""}
	w.Write([]byte("no newline at end"))
	if len(sink.msgs) != 0 {
		t.Errorf("expected nothing logged yet, got: %v", sink.msgs)
	}
}

func TestPrefixWriter_returnsFullLength(t *testing.T) {
	sink := &logSink{}
	w := &prefixWriter{logger: slog.New(sink), prefix: ""}
	data := []byte("line\n")
	n, err := w.Write(data)
	if err != nil || n != len(data) {
		t.Errorf("expected n=%d err=nil, got n=%d err=%v", len(data), n, err)
	}
}

func TestPrefixWriter_emptyWrite(t *testing.T) {
	sink := &logSink{}
	w := &prefixWriter{logger: slog.New(sink), prefix: ""}
	n, err := w.Write([]byte{})
	if err != nil || n != 0 {
		t.Errorf("empty write: n=%d err=%v", n, err)
	}
	if len(sink.msgs) != 0 {
		t.Errorf("no messages expected for empty write")
	}
}
