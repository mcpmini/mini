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

// UseEphemeralCallbackPort makes ListenCallback bind a free IPv4 loopback port so tests that
// drive a full OAuth flow never contend for the fixed production callback port.
func UseEphemeralCallbackPort() {
	callbackListenAddr = func(*config.AuthConfig) string { return "127.0.0.1:0" }
}

func ResetEndpointValidation() {
	endpointValidator = transport.ValidateURL
}

// LoginCodeCh returns the internal code channel so tests can deliver a code
// directly, bypassing HTTP timing and making "Close wins after code buffered" tests deterministic.
func LoginCodeCh(l *BrowserLogin) chan<- string { return l.codeCh }
