package auth

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
)

func configFrom(ac *config.AuthConfig) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     ac.ClientID,
		ClientSecret: ac.ClientSecret,
		Scopes:       ac.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   ac.AuthURL,
			TokenURL:  ac.TokenURL,
			AuthStyle: tokenAuthStyle(ac),
		},
	}
}

func tokenAuthStyle(ac *config.AuthConfig) oauth2.AuthStyle {
	switch ac.TokenEndpointAuthMethod {
	case "client_secret_basic":
		return oauth2.AuthStyleInHeader
	case "client_secret_post", "none":
		return oauth2.AuthStyleInParams
	case "":
		if ac.ClientSecret == "" {
			return oauth2.AuthStyleInParams
		}
	}
	return oauth2.AuthStyleAutoDetect
}

func oauthHTTPContextForClient(ctx context.Context, base *http.Client, resourceURL string) context.Context {
	if resourceURL == "" {
		return context.WithValue(ctx, oauth2.HTTPClient, base)
	}
	client := *base
	client.Transport = resourceTransport{base: effectiveTransport(base), resourceURL: resourceURL}
	return context.WithValue(ctx, oauth2.HTTPClient, &client)
}

func effectiveTransport(c *http.Client) http.RoundTripper {
	if c.Transport == nil {
		return http.DefaultTransport
	}
	return c.Transport
}

type resourceTransport struct {
	base        http.RoundTripper
	resourceURL string
}

func (t resourceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return t.base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close() //nolint:errcheck // the read error is authoritative; this source is no longer used
	if err != nil {
		return nil, err
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	values.Set("resource", t.resourceURL)
	body = []byte(values.Encode())
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return t.base.RoundTrip(clone)
}

// ClientMetadataURL is mini's CIMD document URL — the stable client_id used when
// the authorization server advertises client_id_metadata_document_supported.
// GitHub Pages serves application/json; raw.githubusercontent.com serves text/plain,
// which strict authorization servers reject when fetching client metadata documents.
const ClientMetadataURL = "https://mcpmini.github.io/mini/oauth/client-metadata.json"

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

type RefreshParams struct {
	Server config.ServerConfig
	Login  *Login
}

// Refresh exchanges a refresh token while preserving its issuer, endpoint and client binding.
func Refresh(ctx context.Context, p RefreshParams) (*oauth2.Token, error) {
	if err := p.Login.MatchesConfig(p.Server); err != nil {
		return nil, mismatchedLogin(err)
	}
	flow := newOAuthHTTPFlow(ctx, p.Server)
	defer flow.closeIdleConnections()
	if flow.origin != nil {
		if err := flow.validateRefreshBinding(ctx, p); err != nil {
			return nil, err
		}
	}
	ac := p.Login.RefreshConfig(p.Server.Auth)
	src := configFrom(ac).TokenSource(oauthHTTPContextForClient(ctx, flow.client, ac.ResourceURL), p.Login.Token)
	return src.Token()
}

func (f *oauthHTTPFlow) validateRefreshBinding(ctx context.Context, p RefreshParams) error {
	meta, err := f.discover(ctx, p.Server.URL)
	if err != nil {
		return err
	}
	if meta.Issuer != p.Login.Issuer {
		return mismatchedLogin(fmt.Errorf("private OAuth refresh issuer differs from the saved issuer"))
	}
	if p.Server.Auth != nil && p.Server.Auth.TokenURL == p.Login.TokenURL {
		return nil
	}
	if meta.TokenURL != p.Login.TokenURL {
		return mismatchedLogin(fmt.Errorf("private OAuth refresh token endpoint differs from the saved endpoint"))
	}
	return nil
}
