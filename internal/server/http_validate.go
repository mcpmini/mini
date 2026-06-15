package server

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/mcpmini/mini/internal/transport"
)

// sessionIDPattern accepts UUIDs and similar hex strings: 32–128 chars, hex + hyphens.
// Minimum 32 chars prevents trivially enumerable IDs. Hex count >= 16 ensures
// actual entropy (all-hyphen strings would pass length but fail the entropy check).
var sessionIDPattern = regexp.MustCompile(`^[a-f0-9-]{32,128}$`)

// authorizeDaemon enforces the bearer token when one is configured (daemon mode).
// The stdio and serve --http paths leave daemonAuthToken empty and skip this check.
func (s *Server) authorizeDaemon(w http.ResponseWriter, r *http.Request) bool {
	if s.daemonAuthToken == "" {
		return true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.daemonAuthToken)) == 1 {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// hostIsLoopback rejects DNS-rebinding: a rebound request carries the attacker's
// domain in Host, so only a loopback host-part is allowed. Port is stripped first.
func hostIsLoopback(host string) bool {
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// isSameHost blocks DNS-rebinding attacks: a malicious page can't reach the local daemon
// by redirecting to it, because the browser will set Origin to the attacker's domain.
// We do NOT fall back to X-Forwarded-Host: that header is attacker-controllable and would
// defeat the protection. If r.Host is empty, the check fails conservatively.
func isSameHost(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return r.Host != "" && u.Host == r.Host
}

func parseSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.Header.Get("Mcp-Session-Id")
	if id == "" {
		return transport.NewSessionID(), true
	}
	if !sessionIDPattern.MatchString(id) || nonHyphenCount(id) < 16 {
		http.Error(w, "invalid Mcp-Session-Id", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

func nonHyphenCount(s string) int {
	n := 0
	for i := range len(s) {
		if s[i] != '-' {
			n++
		}
	}
	return n
}

func readLimitedBody(w http.ResponseWriter, body io.Reader) ([]byte, bool) {
	data, err := io.ReadAll(io.LimitReader(body, 1<<20+1))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return nil, false
	}
	if len(data) > 1<<20 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(errorResponse(nil, transport.CodeInvalidParams, "request body exceeds 1MB limit")) //nolint:errcheck
		return nil, false
	}
	return data, true
}
