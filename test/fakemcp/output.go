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
	switch r := result.(type) {
	case responseResult:
		return s.enc.Encode(r.response)
	case rawWriteResult:
		_, err := fmt.Fprintf(s.out, "%s", r.data)
		return err
	case continueResult, exitResult:
		return fmt.Errorf("fault effect has no response to write")
	}
	return fmt.Errorf("missing dispatch result")
}

func (s *outputSink) notifyToolsChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enc.Encode(
		transport.Notification{JsonRPC: transport.JsonRPCVersion, Method: transport.NotificationToolsChanged},
	) //nolint:errcheck
}
