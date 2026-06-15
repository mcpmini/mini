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

func (s *Server) authorizeDaemon(w http.ResponseWriter, r *http.Request) bool {
	if s.daemonAuthToken == "" {
		return true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	// Constant-time compare prevents a timing side-channel from leaking the token
	// byte-by-byte, even though this is localhost-only today.
	if ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.daemonAuthToken)) == 1 {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// A DNS-rebinding attack tricks the browser into resolving the attacker's domain to
// 127.0.0.1, so the request arrives locally but with Host set to the attacker's domain.
// Only allowing loopback Host values blocks this class of attack.
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

// A malicious page can reach the local daemon, but the browser sets Origin to
// the attacker's domain — rejecting mismatched Origins blocks cross-origin access.
// We do NOT fall back to X-Forwarded-Host: it's attacker-controllable.
func rejectCrossOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && !isSameHost(r, origin) {
		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
		return true
	}
	return false
}

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
