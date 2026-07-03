package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

const consoleTailBytes = 2048

type harnessPayload struct {
	OK    json.RawMessage `json:"ok"`
	Error *harnessError   `json:"error"`
}

type harnessError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func classify(result runResult, parentCtx, runCtx context.Context, marker string) (json.RawMessage, error) {
	if result.outputTooLarge {
		return nil, &Error{
			Kind:    KindOutputTooLarge,
			Message: "program output exceeded the 8MB limit",
			Console: consoleTail(result.stdout, result.stderr),
		}
	}

	idx := bytes.LastIndex(result.stdout, []byte(marker))
	if idx < 0 {
		return nil, classifyNoMarker(result, parentCtx, runCtx)
	}
	return parsePayload(result.stdout[idx+len(marker):], consoleTail(result.stdout[:idx], result.stderr))
}

func classifyNoMarker(result runResult, parentCtx, runCtx context.Context) *Error {
	console := consoleTail(result.stdout, result.stderr)
	switch {
	case parentCtx.Err() != nil:
		return &Error{Kind: KindCancelled, Message: "execution cancelled", Console: console}
	case runCtx.Err() != nil:
		return &Error{Kind: KindTimeout, Message: "execution timed out", Console: console}
	case result.waitErr != nil:
		return &Error{Kind: KindRunner, Message: trimStderr(result.stderr, result.waitErr), Console: console}
	default:
		return &Error{Kind: KindRunner, Message: "no result emitted", Console: console}
	}
}

func parsePayload(raw []byte, console string) (json.RawMessage, error) {
	var payload harnessPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, &Error{Kind: KindRunner, Message: "malformed result payload: " + err.Error(), Console: console}
	}
	if payload.Error != nil {
		return nil, &Error{Kind: ErrorKind(payload.Error.Kind), Message: payload.Error.Message, Console: console}
	}
	if payload.OK == nil {
		return json.RawMessage("null"), nil
	}
	return payload.OK, nil
}

func trimStderr(stderr []byte, waitErr error) string {
	if trimmed := strings.TrimSpace(string(stderr)); trimmed != "" {
		return trimmed
	}
	return waitErr.Error()
}

func consoleTail(preMarkerStdout, stderr []byte) string {
	combined := append(append([]byte{}, preMarkerStdout...), stderr...)
	if len(combined) > consoleTailBytes {
		combined = combined[len(combined)-consoleTailBytes:]
	}
	return strings.TrimSpace(string(combined))
}
