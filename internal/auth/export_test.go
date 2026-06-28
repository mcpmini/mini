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

// UseLoopbackURLValidation bypasses the SSRF URL check for one test so it can
// exercise loopback registration endpoints without weakening unrelated tests.
func UseLoopbackURLValidation(t interface{ Cleanup(func()) }) {
	prev := validateEndpointURL
	validateEndpointURL = func(string, string) error { return nil }
	t.Cleanup(func() {
		validateEndpointURL = prev
	})
}
