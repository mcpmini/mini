//go:build integration

package main

import (
	"bufio"
	"encoding/json"
	"os"

	"github.com/mcpmini/mini/internal/transport"
)

func serve(handler *mcpHandler, sink *outputSink) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 16<<20), 16<<20)
	for scanner.Scan() {
		if err := serveRequest(handler, sink, scanner.Bytes()); err != nil {
			return
		}
	}
}

func serveRequest(handler *mcpHandler, sink *outputSink, line []byte) error {
	var req transport.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil
	}
	if req.ID == nil {
		return nil
	}
	result := handler.dispatch(req)
	if result.exit {
		os.Exit(1)
	}
	return sink.writeResult(result)
}
