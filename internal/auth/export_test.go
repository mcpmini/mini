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

// UseLoopbackURLValidation bypasses the SSRF URL check so tests can use httptest loopback endpoints.
func UseLoopbackURLValidation() {
	validateEndpointURL = func(string, string) error { return nil }
}
