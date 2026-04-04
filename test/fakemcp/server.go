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
		var req transport.Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue // notification — no response
		}
		result := handler.dispatch(req)
		if err := writeResult(enc, result); err != nil {
			return
		}
	}
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
