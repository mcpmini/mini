package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
)

var ErrLoginClosed = errors.New("oauth browser login closed")

// BrowserLogin owns a callback listener and drives an OAuth2 PKCE exchange.
type BrowserLogin struct {
	server    *http.Server
	serving   sync.WaitGroup
	results   chan loginCallbackResult
	closed    chan struct{}
	closeOnce sync.Once
	authURL   string
	oauth2Cfg *oauth2.Config
	verifier  string
	login     Login
}

type BeginLoginParams = ResolveEndpointsParams

// BeginLogin binds the callback port before discovery, so a busy port fails before a client is
// registered.
func BeginLogin(ctx context.Context, sc config.ServerConfig, p BeginLoginParams) (*BrowserLogin, error) {
	listener, err := listenCallback(ctx, sc.Auth)
	if err != nil {
		return nil, err
	}
	// A caller's config that gained a discovered client_id would read it as configured and refuse
	// the login it just made.
	sc.Auth = sc.Auth.Clone()
	client, resolveErr := ResolveEndpoints(ctx, &sc, p)
	if resolveErr != nil {
		listener.Close() //nolint:errcheck // listener cleanup cannot replace the endpoint-resolution error returned to the caller
		return nil, fmt.Errorf("resolve oauth endpoints: %w", resolveErr)
	}
	login, err := StartBrowserLogin(sc.Auth, listener, client)
	if err != nil {
		return nil, fmt.Errorf("start oauth login: %w", err)
	}
	return login, nil
}

var callbackListenAddr = func(ac *config.AuthConfig) string {
	return fmt.Sprintf("localhost:%d", ResolvedCallbackPort(ac))
}

func listenCallback(ctx context.Context, ac *config.AuthConfig) (net.Listener, error) {
	addr := callbackListenAddr(ac)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for oauth callback on %s: %w", addr, err)
	}
	return listener, nil
}

// StartBrowserLogin takes ownership of listener, closing it on error. ac must be resolved, since the
// login records its ResourceURL and TokenURL.
func StartBrowserLogin(ac *config.AuthConfig, listener net.Listener, client LoginClient) (*BrowserLogin, error) {
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close() //nolint:errcheck // cleanup must not replace the address-type error
		return nil, fmt.Errorf("oauth callback listener has unexpected address type %T", listener.Addr())
	}
	cfg, verifier, state := buildPKCEConfig(ac, tcpAddr.Port)
	login := &BrowserLogin{
		results: make(chan loginCallbackResult, 1), closed: make(chan struct{}),
		oauth2Cfg: cfg, verifier: verifier,
		login: Login{Resource: ac.ResourceURL, TokenURL: ac.TokenURL, Client: client},
		authURL: buildAuthURL(cfg, buildAuthURLParams{
			state: state, verifier: verifier,
			resourceURL: ac.ResourceURL, extraAuthParams: ac.ExtraAuthParams,
		}),
	}
	login.serve(listener, callbackHandler(state, login.results))
	return login, nil
}

func buildPKCEConfig(ac *config.AuthConfig, callbackPort int) (*oauth2.Config, string, string) {
	cfg := configFrom(ac)
	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()
	cfg.RedirectURL = fmt.Sprintf("http://localhost:%d%s", callbackPort, LoopbackCallbackPath)
	return cfg, verifier, state
}

func (l *BrowserLogin) AuthURL() string { return l.authURL }

// Wait releases the callback port before it returns, so the next flow can bind it.
func (l *BrowserLogin) Wait(ctx context.Context) (*Login, error) {
	code, err := l.awaitCode(ctx)
	l.stopServing()
	if err != nil {
		return nil, err
	}
	token, err := l.oauth2Cfg.Exchange(oauthHTTPContext(ctx, l.login.Resource), code, oauth2.VerifierOption(l.verifier))
	// Close can land while the exchange is in flight; a closed login must not hand out a token.
	if l.isClosed() {
		return nil, ErrLoginClosed
	}
	if err != nil {
		return nil, err
	}
	login := l.login
	login.Token = token
	return &login, nil
}

func (l *BrowserLogin) awaitCode(ctx context.Context) (string, error) {
	select {
	case result := <-l.results:
		if l.isClosed() {
			return "", ErrLoginClosed
		}
		return result.code, result.err
	case <-l.closed:
		return "", ErrLoginClosed
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (l *BrowserLogin) isClosed() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

// Close releases the port and makes Wait return ErrLoginClosed unless Wait already has its
// token; safe to call repeatedly.
func (l *BrowserLogin) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	l.stopServing()
	return nil
}

func (l *BrowserLogin) serve(listener net.Listener, handler http.Handler) {
	l.server = &http.Server{
		Handler:           handler,
		ErrorLog:          log.New(io.Discard, "", 0), // net/http logs retried accept errors; a login must not print
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	l.serving.Go(func() {
		err := l.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			l.reportServeFailure(err)
		}
	})
}

// A callback that already arrived wins: the login can still finish without the server.
func (l *BrowserLogin) reportServeFailure(err error) {
	select {
	case l.results <- loginCallbackResult{err: fmt.Errorf("oauth callback server: %w", err)}:
	default:
	}
}

func (l *BrowserLogin) stopServing() {
	l.server.Close() //nolint:errcheck // Close can precede Serve starting; joining Serve ensures the listener is closed
	l.serving.Wait()
}
