//go:build integration

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mcpmini/mini/internal/transport"
)

func serve(handler *mcpHandler) {
	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 16<<20), 16<<20)
	for scanner.Scan() {
		if err := serveRequest(enc, handler, scanner.Bytes()); err != nil {
			return
		}
	}
}

func serveRequest(enc *json.Encoder, handler *mcpHandler, line []byte) error {
	var req transport.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil
	}
	if req.ID == nil {
		return nil
	}
	return writeResult(enc, handler.dispatch(req))
}

func writeResult(enc *json.Encoder, result dispatchResult) error {
	if result.exit {
		os.Exit(1)
	}
	if result.rawWrite != nil {
		_, err := fmt.Fprintf(os.Stdout, "%s", result.rawWrite)
		return err
	}
	return enc.Encode(result.response)
}
