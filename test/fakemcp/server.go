//go:build integration

package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"

	"github.com/mcpmini/mini/internal/transport"
)

type stdoutWriter struct {
	mu sync.Mutex // request loop and control server write concurrently
	w  io.Writer
}

func (s *stdoutWriter) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(b)
}

func (s *stdoutWriter) notifyToolsChanged() {
	s.Write([]byte(`{"jsonrpc":"2.0","method":"` + transport.NotificationToolsChanged + "\"}\n")) //nolint:errcheck
}

func serve(handler *mcpHandler, out *stdoutWriter) {
	enc := json.NewEncoder(out)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 16<<20), 16<<20)
	for scanner.Scan() {
		if err := serveRequest(enc, out, handler, scanner.Bytes()); err != nil {
			return
		}
	}
}

func serveRequest(enc *json.Encoder, out *stdoutWriter, handler *mcpHandler, line []byte) error {
	var req transport.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil
	}
	if req.ID == nil {
		return nil
	}
	return writeResult(enc, out, handler.dispatch(req))
}

func writeResult(enc *json.Encoder, out *stdoutWriter, result dispatchResult) error {
	if result.exit {
		os.Exit(1)
	}
	if result.rawWrite != nil {
		_, err := out.Write(result.rawWrite)
		return err
	}
	return enc.Encode(result.response)
}
