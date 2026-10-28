//go:build test

package auth

import (
	"context"
	"net"
	"net/http"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func UseLoopbackHTTPClient() {
	noRedirectClient = newAuthHTTPClient()
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	noRedirectClient.Transport = base.Clone()
}

func UseProductionHTTPClient() {
	noRedirectClient = newAuthHTTPClient()
}

func UseLoopbackEndpoints() {
	endpointValidator = func(string) error { return nil }
}

func UseEphemeralCallbackPort() {
	UseCallbackListenAddr("127.0.0.1:0")
}

func UseCallbackListenAddr(addr string) {
	callbackListenAddr = func(*config.AuthConfig) string { return addr }
}

func ResetEndpointValidation() {
	endpointValidator = transport.ValidateURL
}

func BufferCallbackCode(l *BrowserLogin, code string) {
	l.results <- loginCallbackResult{code: code}
}

func Discover(ctx context.Context, serverURL string) (*ServerMeta, error) {
	return (&oauthHTTPFlow{client: noRedirectClient}).discover(ctx, serverURL)
}

func DiscoverAndApply(ctx context.Context, serverURL string, a *config.AuthConfig) (*ServerMeta, error) {
	return (&oauthHTTPFlow{client: noRedirectClient}).discoverAndApply(ctx, serverURL, a)
}

func Register(ctx context.Context, registrationURL, callbackURI string) (RegistrationResult, error) {
	return (&oauthHTTPFlow{client: noRedirectClient}).register(ctx, registrationURL, callbackURI)
}

func StartBrowserLogin(ac *config.AuthConfig, listener net.Listener, resolved ResolvedLogin) (*BrowserLogin, error) {
	if resolved.flow == nil {
		resolved.flow = &oauthHTTPFlow{client: noRedirectClient}
	}
	return startBrowserLogin(ac, listener, resolved)
}
