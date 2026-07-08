//go:build test

package transport

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestScanSSEMessages_multilineData(t *testing.T) {
	stream := "event: message\ndata: {\"jsonrpc\":\"2.0\",\ndata: \"method\":\"notifications/tools/list_changed\"}\n\n"
	var messages []json.RawMessage
	if err := ScanSSEMessages(strings.NewReader(stream), func(message json.RawMessage) error {
		messages = append(messages, message)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d", len(messages))
	}
	var notification Notification
	if err := json.Unmarshal(messages[0], &notification); err != nil {
		t.Fatal(err)
	}
	if notification.Method != NotificationToolsChanged {
		t.Fatalf("method = %q", notification.Method)
	}
}

func TestScanSSEMessages_discardsUnterminatedEvent(t *testing.T) {
	for _, stream := range []string{
		`data: {"ok":true}`,
		"data: {\"ok\":true}\n",
	} {
		called := false
		if err := ScanSSEMessages(strings.NewReader(stream), func(json.RawMessage) error {
			called = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if called {
			t.Fatalf("handler called for %q", stream)
		}
	}
}

func TestScanSSEMessages_preservesDataBytes(t *testing.T) {
	stream := "data: {\"value\":\"a  \"}\n\n" +
		"data:  {\"value\":\"b\"}\n\n"
	var messages []string
	if err := ScanSSEMessages(strings.NewReader(stream), func(message json.RawMessage) error {
		messages = append(messages, string(message))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"value":"a  "}`,
		` {"value":"b"}`,
	}
	if !slices.Equal(messages, want) {
		t.Fatalf("messages = %q, want %q", messages, want)
	}
}

func TestScanSSEMessages_emptyFirstDataLine(t *testing.T) {
	stream := "data:\n" +
		"data: {\"ok\":true}\n\n"
	var messages []string
	if err := ScanSSEMessages(strings.NewReader(stream), func(message json.RawMessage) error {
		messages = append(messages, string(message))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != "\n{\"ok\":true}" {
		t.Fatalf("messages = %q", messages)
	}
}

func TestScanSSEMessages_ignoresCommentsAndOtherFields(t *testing.T) {
	stream := ": comment\n" +
		"event: message\n" +
		"id: 7\n" +
		"data: {\"ok\":true}\n\n"
	var messages []string
	if err := ScanSSEMessages(strings.NewReader(stream), func(message json.RawMessage) error {
		messages = append(messages, string(message))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != `{"ok":true}` {
		t.Fatalf("messages = %q", messages)
	}
}

func TestScanSSEMessages_handlesCRLF(t *testing.T) {
	stream := "data: {\"ok\":true}\r\n\r\n"
	var messages []string
	if err := ScanSSEMessages(strings.NewReader(stream), func(message json.RawMessage) error {
		messages = append(messages, string(message))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != `{"ok":true}` {
		t.Fatalf("messages = %q", messages)
	}
}

func TestScanSSEMessages_handlesCROnlyMultipleEvents(t *testing.T) {
	stream := "data: {\"ok\":1}\r\rdata: {\"ok\":2}\r\r"
	var messages []string
	if err := ScanSSEMessages(strings.NewReader(stream), func(message json.RawMessage) error {
		messages = append(messages, string(message))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{`{"ok":1}`, `{"ok":2}`}
	if !slices.Equal(messages, want) {
		t.Fatalf("messages = %q, want %q", messages, want)
	}
}

func TestScanSSEMessages_handlesMixedLineEndings(t *testing.T) {
	stream := "event: message\r\ndata: {\"ok\":1}\n\rdata: {\"ok\":2}\r\n\r\n"
	var messages []string
	if err := ScanSSEMessages(strings.NewReader(stream), func(message json.RawMessage) error {
		messages = append(messages, string(message))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{`{"ok":1}`, `{"ok":2}`}
	if !slices.Equal(messages, want) {
		t.Fatalf("messages = %q, want %q", messages, want)
	}
}

func TestScanSSEMessages_handlesCRLFAcrossFragmentBoundary(t *testing.T) {
	stream := "data: {\"ok\":true}\r\n\r\n"
	var messages []string
	reader := &chunkedReader{
		data:  []byte(stream),
		sizes: []int{len("data: {\"ok\":true}\r"), 1, 1},
	}
	if err := ScanSSEMessages(reader, func(message json.RawMessage) error {
		messages = append(messages, string(message))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0] != `{"ok":true}` {
		t.Fatalf("messages = %q", messages)
	}
}

func TestScanSSEMessages_skipsInvalidPayloads(t *testing.T) {
	stream := "data: not-json\n\n"
	called := false
	if err := ScanSSEMessages(strings.NewReader(stream), func(json.RawMessage) error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("invalid payload should be ignored")
	}
}

func TestScanSSEMessages_limitBoundaries(t *testing.T) {
	limits := testSSELimits()
	makeLine := func(size int) string {
		return "data: \"" + strings.Repeat("a", size-2) + "\"\n\n"
	}
	makeMultiline := func(total int) string {
		first := (total - 8) / 2
		second := total - 8 - first
		return "data: [\"" + strings.Repeat("a", first) + "\",\n" +
			"data: \"" + strings.Repeat("b", second) + "\"]\n\n"
	}
	cases := []struct {
		name    string
		stream  string
		wantErr error
	}{
		{name: "below_limit", stream: makeLine(limits.messageBytes - 1)},
		{name: "at_limit", stream: makeLine(limits.messageBytes)},
		{name: "above_limit", stream: makeLine(limits.messageBytes + 1), wantErr: errSSEMessageTooLarge},
		{name: "multiline_at_limit", stream: makeMultiline(limits.messageBytes)},
		{name: "multiline_above_limit", stream: makeMultiline(limits.messageBytes + 1), wantErr: errSSEMessageTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			err := scanSSEMessagesWithLimits(strings.NewReader(tc.stream), limits, func(message json.RawMessage) error {
				called = true
				if len(message) > limits.messageBytes {
					t.Fatalf("message len = %d", len(message))
				}
				return nil
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ScanSSEMessages() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && !called {
				t.Fatal("expected parsed message")
			}
			if tc.wantErr != nil && called {
				t.Fatal("overflowing message should not be delivered")
			}
		})
	}
}

func testSSELimits() sseLimits {
	return sseLimits{messageBytes: 64, lineBytes: 72}
}

type chunkedReader struct {
	data  []byte
	sizes []int
	pos   int
	step  int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	size := len(p)
	if r.step < len(r.sizes) && r.sizes[r.step] < size {
		size = r.sizes[r.step]
	}
	if remaining := len(r.data) - r.pos; remaining < size {
		size = remaining
	}
	n := copy(p, r.data[r.pos:r.pos+size])
	r.pos += n
	r.step++
	return n, nil
}
