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
	req, ok := parseRequest(line)
	if !ok {
		return nil
	}
	result := handler.dispatch(req)
	switch result.(type) {
	case exitResult:
		os.Exit(1)
	case continueResult:
		return nil
	case responseResult, rawWriteResult:
		return sink.writeResult(result)
	}
	return nil
}

func parseRequest(line []byte) (transport.Request, bool) {
	var req transport.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return req, false
	}
	return req, req.ID != nil
}
