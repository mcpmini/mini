package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
)

// ErrLoginClosed is returned by Wait when Close has been called.
var ErrLoginClosed = errors.New("oauth browser login closed")

// BrowserLogin owns a callback listener and drives an OAuth2 PKCE exchange.
type BrowserLogin struct {
	srv         *http.Server
	serveDone   chan struct{}
	codeCh      chan string
	authURL     string
	oauth2Cfg   *oauth2.Config
	verifier    string
	resourceURL string
	stopOnce    sync.Once
	closeOnce   sync.Once
	closed      chan struct{}
	resultOnce  sync.Once
	result      loginResult
}

type loginResult struct {
	token *oauth2.Token
	err   error
}

var callbackListenAddr = func(ac *config.AuthConfig) string {
	return fmt.Sprintf("localhost:%d", ResolvedCallbackPort(ac))
}

// ListenCallback binds localhost:ResolvedCallbackPort(ac) — the only place production code binds the callback port.
func ListenCallback(ctx context.Context, ac *config.AuthConfig) (net.Listener, error) {
	addr := callbackListenAddr(ac)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for oauth callback on %s: %w", addr, err)
	}
	return ln, nil
}

// StartBrowserLogin takes ownership of ln: it serves the callback on it and builds the
// authorization URL. On error, ln is closed.
func StartBrowserLogin(ac *config.AuthConfig, ln net.Listener) (*BrowserLogin, error) {
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close() //nolint:errcheck
		return nil, fmt.Errorf("oauth callback listener has unexpected address type %T", ln.Addr())
	}
	cfg, verifier, state := buildPKCEConfig(ac, tcpAddr.Port)
	codeCh := make(chan string, 1)
	serveDone := make(chan struct{})
	srv := startCallbackServer(ln, callbackHandler(state, codeCh), serveDone)
	authURL := buildAuthURL(cfg, buildAuthURLParams{
		state: state, verifier: verifier,
		resourceURL: ac.ResourceURL, extraAuthParams: ac.ExtraAuthParams,
	})
	return &BrowserLogin{
		srv: srv, serveDone: serveDone, codeCh: codeCh,
		authURL: authURL, oauth2Cfg: cfg, verifier: verifier,
		resourceURL: ac.ResourceURL, closed: make(chan struct{}),
	}, nil
}

func buildPKCEConfig(ac *config.AuthConfig, callbackPort int) (*oauth2.Config, string, string) {
	cfg := configFrom(ac)
	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()
	cfg.RedirectURL = fmt.Sprintf("http://localhost:%d%s", callbackPort, LoopbackCallbackPath)
	return cfg, verifier, state
}

func (l *BrowserLogin) AuthURL() string { return l.authURL }

// Wait blocks until a valid callback code arrives, ctx is done, or Close is called.
// Before returning on every path, it stops the callback server and waits for the Serve
// goroutine to exit, so the port is released when Wait returns.
// It stops the server BEFORE the token exchange network call.
// Repeated calls return the same result.
func (l *BrowserLogin) Wait(ctx context.Context) (*oauth2.Token, error) {
	l.resultOnce.Do(func() {
		l.result.token, l.result.err = l.doWait(ctx)
	})
	return l.result.token, l.result.err
}

func (l *BrowserLogin) doWait(ctx context.Context) (*oauth2.Token, error) {
	var code string
	select {
	case code = <-l.codeCh:
	case <-ctx.Done():
		select {
		case code = <-l.codeCh:
			// code arrived just before cancel; use a fresh context for the exchange
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
		default:
			l.stopAndWait()
			return nil, ctx.Err()
		}
	case <-l.closed:
		l.stopAndWait()
		return nil, ErrLoginClosed
	}
	l.stopAndWait()
	// select is non-deterministic when both codeCh and closed are ready simultaneously;
	// re-check closed so Close always wins over a concurrently-buffered code.
	select {
	case <-l.closed:
		return nil, ErrLoginClosed
	default:
	}
	opts := []oauth2.AuthCodeOption{oauth2.VerifierOption(l.verifier)}
	return l.oauth2Cfg.Exchange(oauthHTTPContext(ctx, l.resourceURL), code, opts...)
}

// Close stops the flow: releases the port and makes a pending or future Wait return
// ErrLoginClosed. Idempotent; safe to call concurrently with Wait and after it.
func (l *BrowserLogin) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	l.stopAndWait()
	return nil
}

func (l *BrowserLogin) stopAndWait() {
	l.stopOnce.Do(func() { l.srv.Close() }) //nolint:errcheck
	<-l.serveDone
}

func startCallbackServer(ln net.Listener, handler http.Handler, done chan<- struct{}) *http.Server {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		defer close(done)
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Printf("oauth callback server: %v", err)
		}
	}()
	return srv
}

func callbackHandler(state string, codeCh chan<- string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		writeAuthorizedResponse(w)
		sendAuthCode(codeCh, code)
	})
}

func writeAuthorizedResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintln(w, "<html><body><p>Authorized. You can close this tab.</p></body></html>")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func sendAuthCode(codeCh chan<- string, code string) {
	select {
	case codeCh <- code:
	default:
	}
}
