//go:build test

package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSplitHTTPMessages_SSEWrapped(t *testing.T) {
	got, err := splitHTTPMessages([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(messages) = %d", len(got))
	}
	var payload struct {
		Result map[string]bool `json:"result"`
	}
	if err := json.Unmarshal(got[0], &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Result["ok"] {
		t.Fatalf("result = %v", payload.Result)
	}
}

func TestSplitHTTPMessages_SSEWrappedIDFirst(t *testing.T) {
	got, err := splitHTTPMessages([]byte("id: 7\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(messages) = %d", len(got))
	}
}

func TestSplitHTTPMessages_SSEWrappedCommentFirst(t *testing.T) {
	got, err := splitHTTPMessages([]byte(": keepalive\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len(messages) = %d", len(got))
	}
}

func TestSplitHTTPMessages_SSEWrappedBOM(t *testing.T) {
	got, err := splitHTTPMessages([]byte("\xef\xbb\xbfdata: {\"ok\":true}\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != `{"ok":true}` {
		t.Fatalf("splitHTTPMessages() = %q", got)
	}
}

func TestSplitHTTPMessages_SSEWrappedUnknownFields(t *testing.T) {
	stream := "vendor.field: a:b\nfield with spaces: ignored\ndata: {\"ok\":true}\n\n"
	got, err := splitHTTPMessages([]byte(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != `{"ok":true}` {
		t.Fatalf("splitHTTPMessages() = %q", got)
	}
}

func TestSplitHTTPMessages_SSEWrappedLargePreDataFields(t *testing.T) {
	for _, lineBytes := range []int{(64 << 10) - 1, 64 << 10, (64 << 10) + 1} {
		got, err := splitHTTPMessages(sseEnvelopeWithFieldBytes(lineBytes))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || string(got[0]) != `{"ok":true}` {
			t.Fatalf("line bytes %d: splitHTTPMessages() = %q", lineBytes, got)
		}
	}
}

func TestSplitHTTPMessages_SSEWrappedLineLimit(t *testing.T) {
	got, err := splitHTTPMessages(sseEnvelopeWithFieldBytes(maxSSELineBytes))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != `{"ok":true}` {
		t.Fatalf("splitHTTPMessages() = %q", got)
	}
	_, err = splitHTTPMessages(sseEnvelopeWithFieldBytes(maxSSELineBytes + 1))
	if !errors.Is(err, errSSEMessageTooLarge) {
		t.Fatalf("splitHTTPMessages() error = %v, want %v", err, errSSEMessageTooLarge)
	}
}

func TestSplitHTTPMessages_JSONDoesNotFalsePositiveAsSSE(t *testing.T) {
	payloads := [][]byte{
		[]byte(`{"data":{"ok":true},"event":"message"}`),
		[]byte("{\n  \"event\": \"message\",\n  \"data\": {\"ok\": true}\n}\n"),
	}
	for _, payload := range payloads {
		got, err := splitHTTPMessages(payload)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !bytes.Equal(got[0], bytes.TrimSpace(payload)) {
			t.Fatalf("splitHTTPMessages() = %q", got)
		}
	}
}

func sseEnvelopeWithFieldBytes(lineBytes int) []byte {
	const prefix = "id: "
	if lineBytes < len(prefix) {
		panic("lineBytes too small")
	}
	return []byte(prefix + strings.Repeat("a", lineBytes-len(prefix)) + "\ndata: {\"ok\":true}\n\n")
}
