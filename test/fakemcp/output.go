//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/mcpmini/mini/internal/transport"
)

type outputSink struct {
	mu  sync.Mutex
	out io.Writer
	enc *json.Encoder
}

func newOutputSink(out io.Writer) *outputSink {
	return &outputSink{out: out, enc: json.NewEncoder(out)}
}

func (s *outputSink) writeResult(result dispatchResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.rawWrite != nil {
		_, err := fmt.Fprintf(s.out, "%s", result.rawWrite)
		return err
	}
	return s.enc.Encode(result.response)
}

func (s *outputSink) notifyToolsChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enc.Encode(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationToolsChanged}) //nolint:errcheck
}
