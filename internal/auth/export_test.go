//go:build test

package auth

import (
	"net/http"
	"time"
)

// UseLoopbackHTTPClient bypasses SSRFSafeDialer so tests can reach httptest servers on loopback.
func UseLoopbackHTTPClient() {
	noRedirectClient = &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
