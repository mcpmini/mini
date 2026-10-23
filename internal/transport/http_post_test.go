//go:build test

package transport

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", -1},
		{" 7 ", 7 * time.Second},
		{"0", 0},
		{"3600", 60 * time.Second},
		{"-5", -1},
		{"soon", -1},
		{now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second},
		{now.Add(time.Hour).Format(http.TimeFormat), 60 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), -1},
	}
	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			if got := parseRetryAfter(tc.header, now); got != tc.want {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestNextRetryDelay(t *testing.T) {
	backoff := 2 * time.Second
	if got := nextRetryDelay(5*time.Second, &backoff); got != 5*time.Second || backoff != 2*time.Second {
		t.Fatalf("Retry-After delay = %v, backoff = %v; want the server's delay with backoff untouched", got, backoff)
	}
	if got := nextRetryDelay(0, &backoff); got != 0 || backoff != 2*time.Second {
		t.Fatalf("zero delay = %v, backoff = %v; want an immediate retry, backoff untouched", got, backoff)
	}
	if got := nextRetryDelay(-1, &backoff); got != 2*time.Second || backoff != 4*time.Second {
		t.Fatalf("backoff delay = %v, next backoff = %v; want 2s then 4s", got, backoff)
	}
}

func TestBuildHTTPRequestHeadersPerLegacyMethod(t *testing.T) {
	conn := &HTTPConnection{
		url:             "http://upstream.test/mcp",
		protocolVersion: LegacyVersions[1],
		sessionID:       "old-session",
		headers:         map[string]string{"MCP-Protocol-Version": "custom", "X-Team": "a"},
	}
	tests := []struct {
		method, version, session string
	}{
		{"initialize", "", ""},
		{"server/discover", ModernVersions[0], ""},
		{"tools/list", LegacyVersions[1], "old-session"},
	}
	for _, tc := range tests {
		t.Run(tc.method, func(t *testing.T) {
			req, _, err := conn.buildHTTPRequest(t.Context(), httpCall{Request: Request{Method: tc.method}})
			if err != nil {
				t.Fatal(err)
			}
			if got := req.Header.Get("MCP-Protocol-Version"); got != tc.version {
				t.Errorf("MCP-Protocol-Version = %q, want %q", got, tc.version)
			}
			if got := req.Header.Get("Mcp-Session-Id"); got != tc.session {
				t.Errorf("Mcp-Session-Id = %q, want %q", got, tc.session)
			}
			if req.Header.Get("X-Team") != "a" {
				t.Errorf("configured header missing: %v", req.Header)
			}
		})
	}
}

func TestStoreSessionIDIgnoresOversizedIDs(t *testing.T) {
	conn := &HTTPConnection{sessionID: "kept"}
	conn.storeSessionID(strings.Repeat("x", 257))
	if conn.sessionID != "kept" {
		t.Fatalf("session ID = %q, want an oversized ID ignored", conn.sessionID)
	}
	conn.storeSessionID(strings.Repeat("y", 256))
	if len(conn.sessionID) != 256 {
		t.Fatalf("a 256-byte session ID was not stored")
	}
}
