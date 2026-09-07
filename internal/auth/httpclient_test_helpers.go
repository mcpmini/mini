//go:build test

package auth

import (
	"net/http"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

func UseLoopbackHTTPClient() {
	noRedirectClient = &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// UseLoopbackEndpoints allows discovered OAuth endpoint URLs to be loopback
// addresses. Call in tests that serve both the discovery chain and the token
// endpoint on httptest servers; call ResetEndpointValidation in t.Cleanup.
func UseLoopbackEndpoints() {
	endpointValidator = func(string) error { return nil }
}

// ResetEndpointValidation restores the production SSRF-safe endpoint validator.
func ResetEndpointValidation() {
	endpointValidator = transport.ValidateURL
}
