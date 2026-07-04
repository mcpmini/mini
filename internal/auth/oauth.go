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

func oauthHTTPContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, noRedirectClient)
}

// PKCEFlow performs OAuth2 Authorization Code + PKCE.
// Always prints the auth URL, then also attempts to open it in the browser.
func PKCEFlow(ctx context.Context, ac *config.AuthConfig, openBrowser func(string) error) (*oauth2.Token, error) {
	authURL, doneCh, err := StartPKCEFlow(ctx, ac)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Open this URL in your browser:\n%s\n\n", authURL)
	openBrowser(authURL) //nolint:errcheck
	result := <-doneCh
	return result.Token, result.Err
}

// ClientMetadataURL is mini's CIMD document URL — the stable client_id used when
// the authorization server advertises client_id_metadata_document_supported.
// GitHub Pages serves application/json; raw.githubusercontent.com serves text/plain,
// which strict authorization servers reject when fetching client metadata documents.
const ClientMetadataURL = "https://mcpmini.github.io/mini/oauth/client-metadata.json"

// StartPKCEFlow starts an OAuth2 PKCE flow without blocking.
// Returns the authorization URL and a channel that receives the result when
// the user completes the flow (or ctx is canceled).
// Use StartPKCEFlowOnListener when you need to control listener lifetime.
func StartPKCEFlow(ctx context.Context, ac *config.AuthConfig) (authURL string, done <-chan PKCEResult, err error) {
	addr := fmt.Sprintf("localhost:%d", ResolvedCallbackPort(ac))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("listen for oauth callback on %s: %w", addr, err)
	}
	return StartPKCEFlowOnListener(ctx, ac, listener)
}

// StartPKCEFlowOnListener starts an OAuth2 PKCE flow on a caller-owned listener.
// Closing listener terminates the callback server synchronously, releasing the port
// before any goroutine scheduling. Use this when replacing an existing flow: close
// the old listener first, then bind a new one and call StartPKCEFlowOnListener.
func StartPKCEFlowOnListener(ctx context.Context, ac *config.AuthConfig, listener net.Listener) (authURL string, done <-chan PKCEResult, err error) {
	cfg := configFrom(ac)
	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return "", nil, fmt.Errorf("oauth callback listener has unexpected address type %T", listener.Addr())
	}
	redirectURI := fmt.Sprintf("http://localhost:%d%s", tcpAddr.Port, LoopbackCallbackPath)
	codeCh := make(chan string, 1)
	srv := serveCallbackListener(listener, callbackHandler(state, codeCh))
	cfg.RedirectURL = redirectURI
	url := buildAuthURL(cfg, buildAuthURLParams{
		state:           state,
		verifier:        verifier,
		resourceURL:     ac.ResourceURL,
		extraAuthParams: ac.ExtraAuthParams,
	})

	resultCh := make(chan PKCEResult, 1)
	go exchangeCode(ctx, ExchangeCodeParams{Cfg: cfg, Verifier: verifier, ResourceURL: ac.ResourceURL, CodeCh: codeCh, Srv: srv, ResultCh: resultCh})
	return url, resultCh, nil
}

type buildAuthURLParams struct {
	state, verifier string
	resourceURL     string
	extraAuthParams map[string]string
}

func buildAuthURL(cfg *oauth2.Config, p buildAuthURLParams) string {
	// ExtraAuthParams first so computed security params (resource, code_challenge) always win.
	var opts []oauth2.AuthCodeOption
	for k, v := range p.extraAuthParams {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}
	opts = append(opts, oauth2.S256ChallengeOption(p.verifier))
	if p.resourceURL != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", p.resourceURL))
	}
	return cfg.AuthCodeURL(p.state, opts...)
}

func serveCallbackListener(listener net.Listener, handler http.Handler) *http.Server {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		err := srv.Serve(listener)
		// ErrServerClosed: srv.Close() was called. net.ErrClosed: listener was closed
		// externally by the caller (expected when replacing an in-progress auth flow).
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Printf("oauth callback server: %v", err)
		}
	}()
	return srv
}

type ExchangeCodeParams struct {
	Cfg         *oauth2.Config
	Verifier    string
	ResourceURL string
	CodeCh      <-chan string
	Srv         *http.Server
	ResultCh    chan<- PKCEResult
}

func exchangeCode(ctx context.Context, p ExchangeCodeParams) { //nolint:funclen
	defer p.Srv.Close()
	var code string
	select {
	case code = <-p.CodeCh:
	case <-ctx.Done():
		select {
		case code = <-p.CodeCh:
			// code arrived just before cancel; ctx is already done so use a fresh context
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
		default:
			p.ResultCh <- PKCEResult{Err: ctx.Err()}
			return
		}
	}
	opts := []oauth2.AuthCodeOption{oauth2.VerifierOption(p.Verifier)}
	if p.ResourceURL != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", p.ResourceURL))
	}
	token, err := p.Cfg.Exchange(oauthHTTPContext(ctx), code, opts...)
	p.ResultCh <- PKCEResult{Token: token, Err: err}
}

// Refresh exchanges a refresh token for a new access token.
func Refresh(ctx context.Context, ac *config.AuthConfig, t *oauth2.Token) (*oauth2.Token, error) {
	src := configFrom(ac).TokenSource(oauthHTTPContext(ctx), t)
	return src.Token()
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
