package transport

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestParseHTTPBody_empty(t *testing.T) {
	result, err := parseHTTPBody([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty body, got %s", result)
	}
}

func TestParseHTTPBody_whitespaceOnly(t *testing.T) {
	result, err := parseHTTPBody([]byte("   \n  "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for whitespace body, got %s", result)
	}
}

func TestParseHTTPBody_plainJSON(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	result, err := parseHTTPBody([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if got["tools"] == nil {
		t.Errorf("expected tools in result: %v", got)
	}
}

func TestParseHTTPBody_SSEWrapped(t *testing.T) {
	body := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n"
	result, err := parseHTTPBody([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("expected ok:true in result: %v", got)
	}
}

func TestParseHTTPBody_SSEDataPrefix(t *testing.T) {
	body := "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"val\":42}}\n\n"
	result, err := parseHTTPBody([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
}

func TestParseHTTPBody_malformedJSON(t *testing.T) {
	_, err := parseHTTPBody([]byte(`{"jsonrpc":"2.0", bad json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseHTTPBody_RPCError(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request"}}`
	_, err := parseHTTPBody([]byte(payload))
	if err == nil {
		t.Fatal("expected error for RPC error response")
	}
	if !strings.Contains(err.Error(), "invalid request") {
		t.Errorf("error should mention 'invalid request', got: %v", err)
	}
}

func TestParseHTTPBody_nullResult(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":null}`
	result, err := parseHTTPBody([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = result
}

func TestExtractSSEData_singleDataLine(t *testing.T) {
	body := "event: message\ndata: {\"x\":1}\n\n"
	got := extractSSEData([]byte(body))
	if string(got) != `{"x":1}` {
		t.Errorf("expected {\"x\":1}, got %s", got)
	}
}

func TestExtractSSEData_noDataLine(t *testing.T) {
	body := "event: ping\n\n"
	got := extractSSEData([]byte(body))
	if !strings.Contains(string(got), "ping") {
		t.Errorf("expected fallback to original body, got %s", got)
	}
}

func TestExtractSSEData_multipleDataLines_firstWins(t *testing.T) {
	body := "data: {\"first\":true}\ndata: {\"second\":true}\n"
	got := extractSSEData([]byte(body))
	if string(got) != `{"first":true}` {
		t.Errorf("expected first data line to win, got %s", got)
	}
}

func TestExtractSSEData_leadingWhitespace(t *testing.T) {
	body := "event: message\ndata:   {\"trimmed\":true}  \n"
	got := extractSSEData([]byte(body))
	if string(got) != `{"trimmed":true}` {
		t.Errorf("expected whitespace trimmed, got %s", got)
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
