//go:build test

package auth

import (
	"context"
	"net"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type ResolveEndpointsParams = BeginLoginParams

type ResolvedLogin = resolvedLogin

// ResolveEndpoints releases its temporary HTTP attempt after resolving metadata for tests.
func ResolveEndpoints(ctx context.Context, sc *config.ServerConfig, p BeginLoginParams) (ResolvedLogin, error) {
	resolved, err := resolveEndpoints(ctx, sc, p)
	resolved.flow.closeIdleConnections()
	return resolved, err
}

func UseLoopbackHTTPClient() {
	noRedirectClient = newAuthHTTPClient()
	noRedirectClient.Transport = transport.NewHTTPTransport(transport.SSRFPolicy{AllowPrivateNetworks: true})
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

func ExchangeCallbackCode(l *BrowserLogin, code string) {
	token, err := l.exchange(code)
	l.results <- loginCallbackResult{token: token, err: err}
}

func Discover(ctx context.Context, serverURL string) (*ServerMeta, error) {
	return (&oauthHTTPFlow{client: noRedirectClient, clock: clock.System()}).discover(ctx, serverURL)
}

func DiscoverAndApply(ctx context.Context, serverURL string, a *config.AuthConfig) (*ServerMeta, error) {
	return (&oauthHTTPFlow{client: noRedirectClient, clock: clock.System()}).discoverAndApply(
		ctx,
		discoveryParams{ServerURL: serverURL, Auth: a},
	)
}

func Register(ctx context.Context, registrationURL, callbackURI string) (RegistrationResult, error) {
	return (&oauthHTTPFlow{client: noRedirectClient, clock: clock.System()}).register(ctx, registrationURL, callbackURI)
}

func StartBrowserLogin(ac *config.AuthConfig, listener net.Listener, resolved ResolvedLogin) (*BrowserLogin, error) {
	if resolved.flow == nil {
		resolved.flow = &oauthHTTPFlow{client: noRedirectClient, clock: clock.System()}
	}
	return startBrowserLogin(ac, listener, resolved)
}

func SaveChallenge(dir, name string, challenge Challenge) error {
	return saveChallenge(dir, name, challenge)
}

func LoadChallenge(dir, name, serverURL string) (*Challenge, error) {
	return loadChallenge(dir, name, serverURL)
}
