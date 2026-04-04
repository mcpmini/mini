// White-box tests for upstream content extraction helpers.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestJoinTextContent_empty(t *testing.T) {
	result := joinTextContent(nil)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestJoinTextContent_singleTextItem(t *testing.T) {
	items := contentItems(`[{"type":"text","text":"hello"}]`)
	got := joinTextContent(items)
	if got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
}

func TestJoinTextContent_multipleTextItems(t *testing.T) {
	items := contentItems(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)
	got := joinTextContent(items)
	if got != "a\nb" {
		t.Errorf("expected a\\nb, got %q", got)
	}
}

func TestJoinTextContent_nonTextItemsFiltered(t *testing.T) {
	items := contentItems(`[{"type":"image","text":"ignored"},{"type":"text","text":"kept"}]`)
	got := joinTextContent(items)
	if got != "kept" {
		t.Errorf("expected only text-type items, got %q", got)
	}
}

func TestJoinTextContent_emptyTextSkipped(t *testing.T) {
	items := contentItems(`[{"type":"text","text":""},{"type":"text","text":"real"}]`)
	got := joinTextContent(items)
	if got != "real" {
		t.Errorf("expected empty text to be skipped, got %q", got)
	}
}

func TestExtractContent_plainJSONResult(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}],"isError":false}`)
	result, err := extractContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("expected ok:true, got %v", got)
	}
}

func TestExtractContent_isErrorTrue(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"permission denied"}],"isError":true}`)
	_, err := extractContent(raw)
	if err == nil {
		t.Fatal("expected error for isError:true")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestExtractContent_errorMessageInContent(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"boom"}],"isError":true}`)
	_, err := extractContent(raw)
	if err == nil || err.Error() == "" {
		t.Fatal("expected error with message")
	}
	if !containsStr(err.Error(), "boom") {
		t.Errorf("expected error to contain 'boom', got %q", err.Error())
	}
}

func TestExtractContent_nonJSONTextPassedAsString(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"plain text result"}],"isError":false}`)
	result, err := extractContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be JSON-encoded string
	var s string
	if err := json.Unmarshal(result, &s); err != nil {
		t.Fatalf("expected JSON string, got: %s — err: %v", result, err)
	}
	if s != "plain text result" {
		t.Errorf("expected plain text result, got %q", s)
	}
}

func TestExtractContent_emptyContent(t *testing.T) {
	raw := json.RawMessage(`{"content":[],"isError":false}`)
	result, err := extractContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty content → empty string marshalled to ""
	var s string
	if err := json.Unmarshal(result, &s); err != nil {
		t.Fatalf("expected JSON string: %v", err)
	}
	if s != "" {
		t.Errorf("expected empty string for empty content, got %q", s)
	}
}

func TestExtractContent_invalidJSON_returnsError(t *testing.T) {
	raw := json.RawMessage(`not valid json`)
	_, err := extractContent(raw)
	if err == nil {
		t.Fatal("expected error for non-standard upstream response")
	}
}

func TestExtractContent_arbitraryObject_emptyContent(t *testing.T) {
	// A JSON object that isn't a tools/call envelope (no content field) gets
	// treated as isError=false with empty content → returns empty string.
	raw := json.RawMessage(`{"arbitrary":"data"}`)
	result, err := extractContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var s string
	if err := json.Unmarshal(result, &s); err != nil {
		t.Fatalf("expected JSON string result, got %s: %v", result, err)
	}
	if s != "" {
		t.Errorf("expected empty string for object with no content field, got %q", s)
	}
}

func TestExtractContent_whitespaceTrimmed(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"  {\"x\":1}  "}],"isError":false}`)
	result, err := extractContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("expected JSON after whitespace trim: %v", err)
	}
}

func TestIsConnError_true(t *testing.T) {
	err := connError{err: errors.New("broken pipe")}
	if !isConnError(err) {
		t.Error("expected isConnError=true for connError")
	}
}

func TestIsConnError_false(t *testing.T) {
	if isConnError(errors.New("regular error")) {
		t.Error("expected isConnError=false for regular error")
	}
}

func TestIsConnError_wrapped(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", connError{err: errors.New("inner")})
	if !isConnError(wrapped) {
		t.Error("expected isConnError=true for wrapped connError")
	}
}

func TestIsConnError_nil(t *testing.T) {
	if isConnError(nil) {
		t.Error("expected isConnError=false for nil")
	}
}

// helpers

func contentItems(raw string) []transport.ContentItem {
	var items []transport.ContentItem
	json.Unmarshal([]byte(raw), &items)
	return items
}

func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

