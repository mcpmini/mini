package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

var noRedirectClient = newAuthHTTPClient()

// ServerMeta contains OAuth endpoints discovered for an MCP server.
type ServerMeta struct {
	Issuer                string
	IssParameterSupported bool
	AuthURL               string
	TokenURL              string
	RegistrationURL       string
	CIMDSupported         bool
	RequiredScopes        []string
	Scopes                []string // from WWW-Authenticate scope param or PRM scopes_supported (spec priority)
	Resource              string
}

// asRef is the result of authorization-server discovery: which AS to use and what scopes it suggests.
type asRef struct {
	URL            string
	Scopes         []string
	RequiredScopes []string
	Resource       string
}

// WWW-Authenticate scope takes priority over PRM scopes_supported per MCP spec §Scope Selection Strategy.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/977e7481/docs/specification/2025-11-25/basic/authorization.mdx?plain=1#L333-L340
func preferScopes(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

// DiscoverServer discovers OAuth metadata using the configured server's network provenance.
func DiscoverServer(ctx context.Context, sc config.ServerConfig) (*ServerMeta, error) {
	flow := newOAuthHTTPFlow(ctx, sc)
	defer flow.closeIdleConnections()
	return flow.discover(ctx, sc.URL)
}

func (f *oauthHTTPFlow) discover(ctx context.Context, serverURL string) (*ServerMeta, error) {
	return f.discoverMetadata(ctx, serverURL, nil)
}

func (f *oauthHTTPFlow) discoverMetadata(
	ctx context.Context,
	serverURL string,
	challenge *Challenge,
) (*ServerMeta, error) {
	if err := requireHTTPURL(serverURL); err != nil {
		return nil, err
	}
	ref, err := f.discoverASURL(ctx, serverURL, challenge)
	if err != nil {
		return nil, err
	}

	meta, err := f.discoverASMeta(ctx, ref.URL)
	if err != nil {
		return nil, err
	}
	if ref.Resource == "" {
		ref.Resource, err = CanonicalResourceURI(serverURL)
		if err != nil {
			return nil, err
		}
	}
	meta.Scopes = preferScopes(ref.Scopes, meta.Scopes)
	meta.RequiredScopes = ref.RequiredScopes
	meta.Resource = ref.Resource
	return meta, nil
}

func (f *oauthHTTPFlow) discoverASURL(ctx context.Context, serverURL string, saved *Challenge) (asRef, error) {
	challenge := saved
	if f.needsFreshChallenge(challenge) {
		challenge = f.probeAuthorizationChallenge(ctx, serverURL)
	}
	ref, err := f.discoverFromChallenge(ctx, serverURL, challenge)
	if saved != nil {
		ref = withRequiredScopes(ref, saved.WWWAuthenticate)
	}
	return ref, err
}

func (f *oauthHTTPFlow) needsFreshChallenge(saved *Challenge) bool {
	return f.origin != nil || saved == nil || parseWWWAuthParam(saved.WWWAuthenticate, "resource_metadata") == ""
}

func (f *oauthHTTPFlow) discoverFromChallenge(
	ctx context.Context,
	serverURL string,
	challenge *Challenge,
) (asRef, error) {
	if challenge == nil {
		return f.asURLFromPRMProbe(ctx, serverURL)
	}
	ref, err := f.asRefFromChallenge(ctx, serverURL, challenge)
	if err != nil || ref.URL != "" {
		return ref, err
	}
	ref, err = f.asURLFromPRMProbe(ctx, serverURL)
	return withRequiredScopes(ref, challenge.WWWAuthenticate), err
}

func withRequiredScopes(ref asRef, header string) asRef {
	if scopes := strings.Fields(parseWWWAuthParam(header, "scope")); len(scopes) > 0 {
		ref.Scopes, ref.RequiredScopes = scopes, scopes
	}
	return ref
}

func (f *oauthHTTPFlow) probeAuthorizationChallenge(ctx context.Context, serverURL string) *Challenge {
	err := transport.ProbeAuthorization(ctx, transport.ProbeAuthorizationParams{
		URL: serverURL, Client: f.client, Clock: f.clock,
	})
	var unauthorized *transport.UnauthorizedError
	if errors.As(err, &unauthorized) &&
		ValidAuthorizationChallenge(http.StatusUnauthorized, unauthorized.WWWAuthenticate) {
		return &Challenge{
			ServerURL: serverURL, StatusCode: http.StatusUnauthorized, WWWAuthenticate: unauthorized.WWWAuthenticate,
		}
	}
	// Failed probes fall back to well-known protected-resource metadata discovery.
	return nil
}

func (f *oauthHTTPFlow) asRefFromChallenge(ctx context.Context, serverURL string, challenge *Challenge) (asRef, error) {
	header := challenge.WWWAuthenticate
	ref := withRequiredScopes(asRef{}, header)
	rmURL := parseWWWAuthParam(header, "resource_metadata")
	if rmURL == "" {
		return ref, nil
	}
	prmRef, err := f.fetchASURLFromPRM(ctx, prmFetchParams{MetadataURL: rmURL, ExpectedResource: serverURL})
	return withRequiredScopes(prmRef, header), err
}

func (f *oauthHTTPFlow) asURLFromPRMProbe(ctx context.Context, serverURL string) (asRef, error) {
	discovery, err := parsePRMDiscovery(serverURL)
	if err != nil {
		return asRef{}, err
	}
	return f.probePRMCandidates(ctx, discovery)
}

func (f *oauthHTTPFlow) discoverASMeta(ctx context.Context, asURL string) (*ServerMeta, error) {
	u, err := url.Parse(asURL)
	if err != nil {
		return nil, fmt.Errorf("parse AS URL: %w", err)
	}
	for _, candidate := range asMetaCandidates(u) {
		meta, err := f.fetchASMeta(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if meta != nil {
			return meta, requireIssuer(meta, asURL, candidate)
		}
	}
	return fallbackMeta(u.Scheme + "://" + u.Host), nil
}

// asMetaCandidates returns well-known probe URLs for the given AS URL.
// The MCP spec mandates trying multiple candidates because RFC 8414 and OpenID Connect
// use different conventions for path-based issuers, and real ASes implement both.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/977e7481/docs/specification/2025-11-25/basic/authorization.mdx?plain=1#L131-L144
func asMetaCandidates(u *url.URL) []string {
	base := u.Scheme + "://" + u.Host
	trimmed := strings.Trim(u.Path, "/")
	if trimmed == "" {
		return []string{
			base + "/.well-known/oauth-authorization-server",
			base + "/.well-known/openid-configuration",
		}
	}
	return []string{
		base + "/.well-known/oauth-authorization-server/" + trimmed,
		base + "/.well-known/openid-configuration/" + trimmed,
		base + "/" + trimmed + "/.well-known/openid-configuration",
	}
}

type rawASMeta struct {
	Issuer                string   `json:"issuer"`
	IssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
	AuthURL               string   `json:"authorization_endpoint"`
	TokenURL              string   `json:"token_endpoint"`
	RegistrationURL       string   `json:"registration_endpoint"`
	CIMDSupported         bool     `json:"client_id_metadata_document_supported"`
	PKCEMethods           []string `json:"code_challenge_methods_supported"`
}

func (f *oauthHTTPFlow) fetchASMeta(ctx context.Context, metaURL string) (*ServerMeta, error) {
	resp, err := f.doDiscoveryRequest(ctx, metaURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("fetch AS metadata from %s: %w", metaURL, err)
	}
	//nolint:errcheck // status and metadata determine the result; Close releases the response stream.
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth discovery: status %d from %s", resp.StatusCode, metaURL)
	}
	return decodeASMeta(resp.Body, metaURL)
}

func decodeASMeta(body io.Reader, metaURL string) (*ServerMeta, error) {
	var raw rawASMeta
	if err := json.NewDecoder(io.LimitReader(body, maxAuthBodyBytes)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("oauth discovery: decode metadata from %s: %w", metaURL, err)
	}
	// MCP spec MUST: refuse if code_challenge_methods_supported is absent or lacks S256.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/977e7481/docs/specification/2025-11-25/basic/authorization.mdx?plain=1#L603-L607
	// Applies only to fetched AS metadata; fallback path proceeds without — mini always sends S256, so the AS rejects if unsupported.
	if !pkceS256Supported(raw.PKCEMethods) {
		return nil, fmt.Errorf(
			"oauth discovery: authorization server %s does not support PKCE S256 (code_challenge_methods_supported=%v)",
			metaURL,
			raw.PKCEMethods,
		)
	}
	return &ServerMeta{
		Issuer:                raw.Issuer,
		IssParameterSupported: raw.IssParameterSupported,
		AuthURL:               raw.AuthURL,
		TokenURL:              raw.TokenURL,
		RegistrationURL:       raw.RegistrationURL,
		CIMDSupported:         raw.CIMDSupported,
	}, nil
}

var errIssuerMismatch = errors.New("authorization server metadata issuer mismatch")

// requireIssuer refuses metadata naming another issuer, which could steer a login's code and
// tokens to a server other than the one the MCP server named. The spec forbids normalizing either.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/8e12bf3c/docs/specification/2026-07-28/basic/authorization/authorization-server-discovery.mdx?plain=1#L94
func requireIssuer(meta *ServerMeta, issuer, metaURL string) error {
	if meta.Issuer != issuer {
		return fmt.Errorf(
			"oauth discovery: %w: metadata at %s names issuer %q, not %q",
			errIssuerMismatch,
			metaURL,
			meta.Issuer,
			issuer,
		)
	}
	return nil
}

func pkceS256Supported(methods []string) bool {
	for _, m := range methods {
		if m == "S256" {
			return true
		}
	}
	return false
}

func fallbackMeta(base string) *ServerMeta {
	return &ServerMeta{
		AuthURL:  base + "/authorize",
		TokenURL: base + "/token",
	}
}

func (f *oauthHTTPFlow) doDiscoveryRequest(ctx context.Context, metaURL string) (*http.Response, error) {
	if err := requireHTTPURL(metaURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	return f.client.Do(req)
}

// RequiresOAuth reports whether a server's 401 is confirmed to need OAuth.
func RequiresOAuth(ctx context.Context, sc config.ServerConfig, wwwAuthenticate string) bool {
	serverURL := sc.URL
	if bearerChallenge(wwwAuthenticate) {
		return true
	}
	if strings.TrimSpace(wwwAuthenticate) != "" {
		// A non-Bearer scheme (Basic, Digest, ...) is decisive on its own — a PRM document
		// that happens to exist at the same origin doesn't apply to this specific challenge.
		return false
	}
	f := newOAuthHTTPFlow(ctx, sc)
	defer f.closeIdleConnections()
	discovery, err := parsePRMDiscovery(serverURL)
	if err != nil {
		return false
	}
	for _, c := range discovery.Candidates {
		//nolint:errcheck // a failed candidate does not rule out another PRM discovery URL.
		if ref, _ := f.fetchASURLFromPRM(ctx, c); ref.URL != "" {
			return true
		}
	}
	return false
}
