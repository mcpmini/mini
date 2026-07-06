//go:build test

package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestSplitHTTPMessages_empty(t *testing.T) {
	got, err := splitHTTPMessages([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestSplitHTTPMessages_whitespaceOnly(t *testing.T) {
	got, err := splitHTTPMessages([]byte("   \n  "))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestSplitHTTPMessages_plainJSON(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	got, err := splitHTTPMessages(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatalf("splitHTTPMessages() = %q", got)
	}
}

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
	cases := []struct {
		name      string
		lineBytes int
	}{
		{name: "below_64kib", lineBytes: (64 << 10) - 1},
		{name: "at_64kib", lineBytes: 64 << 10},
		{name: "above_64kib", lineBytes: (64 << 10) + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitHTTPMessages(sseEnvelopeWithFieldBytes(tc.lineBytes))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || string(got[0]) != `{"ok":true}` {
				t.Fatalf("splitHTTPMessages() = %q", got)
			}
		})
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
	cases := []struct {
		name    string
		payload []byte
	}{
		{name: "single_line_object", payload: []byte(`{"data":{"ok":true},"event":"message"}`)},
		{name: "pretty_object", payload: []byte("{\n  \"event\": \"message\",\n  \"data\": {\"ok\": true}\n}\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitHTTPMessages(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || !bytes.Equal(got[0], bytes.TrimSpace(tc.payload)) {
				t.Fatalf("splitHTTPMessages() = %q", got)
			}
		})
	}
}

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
		{name: "below_limit", stream: makeLine(maxSSEMessageBytes - 1)},
		{name: "at_limit", stream: makeLine(maxSSEMessageBytes)},
		{name: "above_limit", stream: makeLine(maxSSEMessageBytes + 1), wantErr: errSSEMessageTooLarge},
		{name: "multiline_at_limit", stream: makeMultiline(maxSSEMessageBytes)},
		{name: "multiline_above_limit", stream: makeMultiline(maxSSEMessageBytes + 1), wantErr: errSSEMessageTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			err := ScanSSEMessages(strings.NewReader(tc.stream), func(message json.RawMessage) error {
				called = true
				if len(message) > maxSSEMessageBytes {
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

func TestParseRetryAfter_seconds(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("30", now)
	if d != 30*time.Second {
		t.Errorf("expected 30s, got %v", d)
	}
}

func TestParseRetryAfter_zero(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("0", now)
	if d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
}

func TestParseRetryAfter_empty(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("", now)
	if d != -1 {
		t.Errorf("expected -1 for empty, got %v", d)
	}
}

func TestParseRetryAfter_httpDate_future(t *testing.T) {
	now := clock.NewFake().Now()
	future := now.Add(5 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(future, now)
	if d <= 0 {
		t.Errorf("expected positive duration for future date, got %v", d)
	}
}

func TestParseRetryAfter_httpDate_past(t *testing.T) {
	now := clock.NewFake().Now()
	past := now.Add(-5 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(past, now)
	if d != -1 {
		t.Errorf("expected -1 for past date, got %v", d)
	}
}

func TestParseRetryAfter_invalid(t *testing.T) {
	now := clock.NewFake().Now()
	d := parseRetryAfter("not-a-date", now)
	if d != -1 {
		t.Errorf("expected -1 for invalid value, got %v", d)
	}
}

type chunkedReader struct {
	data  []byte
	sizes []int
	pos   int
	step  int
}

func sseEnvelopeWithFieldBytes(lineBytes int) []byte {
	const prefix = "id: "
	if lineBytes < len(prefix) {
		panic("lineBytes too small")
	}
	return []byte(prefix + strings.Repeat("a", lineBytes-len(prefix)) + "\ndata: {\"ok\":true}\n\n")
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
	copy(p, r.data[r.pos:r.pos+size])
	r.pos += size
	r.step++
	return size, nil
}
