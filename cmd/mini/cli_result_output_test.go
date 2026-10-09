//go:build test

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

type resultOutputWriter struct {
	match string
	err   error
	buf   bytes.Buffer
}

func (w *resultOutputWriter) Write(p []byte) (int, error) {
	if w.match == "" || bytes.Contains(p, []byte(w.match)) {
		return 0, w.err
	}
	return w.buf.Write(p)
}

var errResultOutput = errors.New("result output failed")

func TestPrintToolTableReturnsFlushError(t *testing.T) {
	err := printToolTable(&resultOutputWriter{err: errResultOutput}, []transport.ToolDefinition{{Name: "tool"}})
	if !errors.Is(err, errResultOutput) {
		t.Fatalf("printToolTable error = %v, want wrapped writer error", err)
	}
}

func TestPrintToolDetailReturnsDirectWriteError(t *testing.T) {
	err := printToolDetail(&resultOutputWriter{err: errResultOutput}, transport.ToolDefinition{Name: "tool"})
	if !errors.Is(err, errResultOutput) {
		t.Fatalf("printToolDetail error = %v, want wrapped writer error", err)
	}
}

func TestRunListReturnsNoServersWriteError(t *testing.T) {
	err := runList(t.TempDir(), nil, &resultOutputWriter{err: errResultOutput})
	if !errors.Is(err, errResultOutput) {
		t.Fatalf("runList error = %v, want writer error", err)
	}
}

func TestRunListReturnsServerTableFlushError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "server", Command: "cmd"})
	err := runList(dir, nil, &resultOutputWriter{err: errResultOutput})
	if !errors.Is(err, errResultOutput) {
		t.Fatalf("runList error = %v, want server table writer error", err)
	}
}

func TestPrintStatusTableReturnsRowFlushError(t *testing.T) {
	disabled := false
	failed, err := printStatusTable(statusTableParams{
		Context: context.Background(),
		Out:     &resultOutputWriter{err: errResultOutput},
		Servers: config.Servers{Loaded: []config.ServerConfig{{Name: "disabled", Enabled: &disabled}}},
	})
	if !errors.Is(err, errResultOutput) {
		t.Fatalf("printStatusTable error = %v, want wrapped writer error", err)
	}
	if failed {
		t.Fatal("disabled server without projection errors reported unhealthy")
	}
}

func TestPrintTestResultsReturnsSummaryWriteError(t *testing.T) {
	writer := &resultOutputWriter{match: "passed, 0 failed", err: errResultOutput}
	err := printTestResults(writer, []upstreamResult{{name: "server"}})
	if !errors.Is(err, errResultOutput) {
		t.Fatalf("printTestResults error = %v, want wrapped writer error", err)
	}
}

func TestPrintTestResultsReturnsHealthFailureAfterSummary(t *testing.T) {
	writer := new(bytes.Buffer)
	err := printTestResults(writer, []upstreamResult{{name: "server", err: io.EOF}})
	if err == nil || err.Error() != "1 server(s) failed" {
		t.Fatalf("printTestResults error = %v, want health failure summary", err)
	}
	if got := writer.String(); !strings.Contains(got, "0 passed, 1 failed") || !strings.Contains(got, "FAIL") {
		t.Fatalf("health failure output missing completed summary: %q", got)
	}
}

func TestPrintCallOutputReturnsJSONEncodingError(t *testing.T) {
	var err error
	output := testutil.CaptureStdout(t, func() {
		err = printCallOutput(
			"server",
			"tool",
			&response.Envelope{Data: map[string]any{"bad": make(chan int)}},
			callOutputJSON,
		)
	})
	var typeErr *json.UnsupportedTypeError
	if !errors.As(err, &typeErr) || !strings.Contains(err.Error(), "encode JSON response") {
		t.Fatalf("printCallOutput error = %v, want wrapped JSON encoding error", err)
	}
	if output != "" {
		t.Fatalf("encoding error printed output: %q", output)
	}
}
