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

func UseLoopbackEndpoints() {
	endpointValidator = func(string) error { return nil }
}

func ResetEndpointValidation() {
	endpointValidator = transport.ValidateURL
}

const ProactiveRefreshBackoff = proactiveRefreshBackoff
