package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
)

// PKCEResult is the outcome of an OAuth2 PKCE flow.
type PKCEResult struct {
	Token *oauth2.Token
	Err   error
}

func configFrom(ac *config.AuthConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     ac.ClientID,
		ClientSecret: ac.ClientSecret,
		Scopes:       ac.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  ac.AuthURL,
			TokenURL: ac.TokenURL,
		},
	}
}

// PKCEFlow performs OAuth2 Authorization Code + PKCE.
// openBrowser is called with the auth URL; if it returns an error the URL is
// printed to stdout for manual copy-paste.
func PKCEFlow(ctx context.Context, ac *config.AuthConfig, openBrowser func(string) error) (*oauth2.Token, error) {
	authURL, doneCh, err := StartPKCEFlow(ctx, ac)
	if err != nil {
		return nil, err
	}
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Open this URL in your browser:\n%s\n\n", authURL)
	}
	result := <-doneCh
	return result.Token, result.Err
}

// StartPKCEFlow starts an OAuth2 PKCE flow without blocking.
// Returns the authorization URL and a channel that receives the result when
// the user completes the flow (or ctx is canceled).
func StartPKCEFlow(ctx context.Context, ac *config.AuthConfig) (authURL string, done <-chan PKCEResult, err error) {
	cfg := configFrom(ac)
	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()

	cb, err := startCallbackServer(state)
	if err != nil {
		return "", nil, err
	}
	cfg.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", cb.port)
	url := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))

	resultCh := make(chan PKCEResult, 1)
	go exchangeCode(ctx, cfg, verifier, cb.codeCh, cb.srv, resultCh)
	return url, resultCh, nil
}

type callbackServerResult struct {
	srv    *http.Server
	codeCh chan string
	port   int
}

func startCallbackServer(state string) (callbackServerResult, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return callbackServerResult{}, fmt.Errorf("listen for callback: %w", err)
	}
	port, err := callbackPort(listener)
	if err != nil {
		listener.Close()
		return callbackServerResult{}, err
	}
	codeCh := make(chan string, 1)
	srv := serveCallbackListener(listener, callbackHandler(state, codeCh))
	return callbackServerResult{srv: srv, codeCh: codeCh, port: port}, nil
}

func serveCallbackListener(listener net.Listener, handler http.Handler) *http.Server {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("oauth callback server: %v", err)
		}
	}()
	return srv
}

func callbackPort(listener net.Listener) (int, error) {
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("listener address is not TCP: %T", listener.Addr())
	}
	return tcpAddr.Port, nil
}

func exchangeCode(ctx context.Context, cfg *oauth2.Config, verifier string, codeCh chan string, srv *http.Server, resultCh chan PKCEResult) {
	defer srv.Close()
	var code string
	select {
	case code = <-codeCh:
	case <-ctx.Done():
		resultCh <- PKCEResult{Err: ctx.Err()}
		return
	}
	token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	resultCh <- PKCEResult{Token: token, Err: err}
}

// Refresh exchanges a refresh token for a new access token.
func Refresh(ctx context.Context, ac *config.AuthConfig, t *oauth2.Token) (*oauth2.Token, error) {
	src := configFrom(ac).TokenSource(ctx, t)
	return src.Token()
}

func callbackHandler(state string, codeCh chan<- string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		writeAuthorizedResponse(w)
		sendAuthCode(codeCh, q.Get("code"))
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
