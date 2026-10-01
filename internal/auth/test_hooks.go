//go:build test

package auth

import (
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func UseLoopbackHTTPClient() {
	noRedirectClient = transport.NewNoRedirectClient(transport.NoRedirectClientOptions{Timeout: 30 * time.Second})
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
