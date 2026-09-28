//go:build test

package auth

import (
	"net/http"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func UseLoopbackHTTPClient() {
	noRedirectClient = &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func UseLoopbackEndpoints() {
	endpointValidator = func(string) error { return nil }
}

func UseEphemeralCallbackPort() {
	callbackListenAddr = func(*config.AuthConfig) string { return "127.0.0.1:0" }
}

func ResetEndpointValidation() {
	endpointValidator = transport.ValidateURL
}

func BufferCallbackCode(l *BrowserLogin, code string) { l.codes <- code }
