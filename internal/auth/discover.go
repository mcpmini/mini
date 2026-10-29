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
	Scopes                []string // from WWW-Authenticate scope param or PRM scopes_supported (spec priority)
	Resource              string
}

// asRef is the result of authorization-server discovery: which AS to use and what scopes it suggests.
type asRef struct {
	URL      string
	Scopes   []string
	Resource string
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
	ref, err := f.discoverASURL(ctx, serverURL)
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
	meta.Resource = ref.Resource
	return meta, nil
}

func (f *oauthHTTPFlow) discoverASURL(ctx context.Context, serverURL string) (asRef, error) {
	wwwRef, err := f.asURLFromWWWAuthenticate(ctx, serverURL)
	if err != nil || wwwRef.URL != "" {
		return wwwRef, err
	}
	probeRef, err := f.asURLFromPRMProbe(ctx, serverURL)
	probeRef.Scopes = preferScopes(wwwRef.Scopes, probeRef.Scopes)
	return probeRef, err
}

func (f *oauthHTTPFlow) asURLFromWWWAuthenticate(ctx context.Context, serverURL string) (asRef, error) {
	resp, err := f.doDiscoveryRequest(ctx, serverURL)
	if err != nil {
		if ctx.Err() != nil {
			return asRef{}, ctx.Err()
		}
		return asRef{}, nil
	}
	//nolint:errcheck // discovery status and headers determine the result; Close releases the response stream.
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return asRef{}, nil
	}
	header := resp.Header.Get("WWW-Authenticate")
	rmURL := parseWWWAuthParam(header, "resource_metadata")
	scopesFromHeader := strings.Fields(parseWWWAuthParam(header, "scope"))
	if rmURL == "" {
		return asRef{Scopes: scopesFromHeader}, nil
	}
	prmRef, err := f.fetchASURLFromPRM(ctx, prmFetchParams{MetadataURL: rmURL, ExpectedResource: serverURL})
	prmRef.Scopes = preferScopes(scopesFromHeader, prmRef.Scopes)
	return prmRef, err
}

func parseWWWAuthParam(header, param string) string {
	// RFC 6750: the auth-scheme token (e.g. "Bearer") precedes the key=value params.
	rest := header
	if i := strings.IndexByte(header, ' '); i >= 0 {
		rest = header[i+1:]
	}
	for _, field := range strings.Split(rest, ",") {
		field = strings.TrimSpace(field)
		val, ok := strings.CutPrefix(field, param+`="`)
		if ok {
			return strings.TrimSuffix(val, `"`)
		}
	}
	return ""
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

// RequiresOAuth reports whether a server's 401 is confirmed (per RFC 9728) to need OAuth.
func RequiresOAuth(ctx context.Context, sc config.ServerConfig, wwwAuthenticate string) bool {
	serverURL := sc.URL
	scheme := strings.ToLower(strings.TrimSpace(wwwAuthenticate))
	if strings.HasPrefix(scheme, "bearer") {
		return true
	}
	if scheme != "" {
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
