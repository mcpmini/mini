package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

// noRedirectClient is shared by discovery and registration. It blocks redirects
// (prevents session-token exfiltration) and uses SSRFSafeDialer (prevents discovery
// from probing internal network endpoints via attacker-controlled metadata URLs).
var noRedirectClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext: transport.SSRFSafeDialer(),
	},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ServerMeta contains OAuth endpoints discovered for an MCP server.
type ServerMeta struct {
	AuthURL         string
	TokenURL        string
	RegistrationURL string
	CIMDSupported   bool
	Scopes          []string // from WWW-Authenticate scope param or PRM scopes_supported (spec priority)
}

// asRef is the result of authorization-server discovery: which AS to use and what scopes it suggests.
type asRef struct {
	URL    string
	Scopes []string
}

// WWW-Authenticate scope takes priority over PRM scopes_supported per MCP spec §Scope Selection Strategy.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/977e7481/docs/specification/2025-11-25/basic/authorization.mdx?plain=1#L333-L340
func preferScopes(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

// Discover resolves OAuth endpoint metadata for an MCP server via RFC 9728 + RFC 8414.
func Discover(ctx context.Context, serverURL string) (*ServerMeta, error) {
	ref, err := discoverASURL(ctx, serverURL)
	if err != nil {
		return nil, err
	}
	meta, err := discoverASMeta(ctx, ref.URL)
	if err != nil {
		return nil, err
	}
	meta.Scopes = preferScopes(ref.Scopes, meta.Scopes)
	return meta, nil
}

func discoverASURL(ctx context.Context, serverURL string) (asRef, error) {
	wwwRef, err := asURLFromWWWAuthenticate(ctx, serverURL)
	if err != nil || wwwRef.URL != "" {
		return wwwRef, err
	}
	probeRef, err := asURLFromPRMProbe(ctx, serverURL)
	probeRef.Scopes = preferScopes(wwwRef.Scopes, probeRef.Scopes)
	return probeRef, err
}

func asURLFromWWWAuthenticate(ctx context.Context, serverURL string) (asRef, error) {
	resp, err := doDiscoveryRequest(ctx, serverURL)
	if err != nil {
		if ctx.Err() != nil {
			return asRef{}, ctx.Err()
		}
		return asRef{}, nil
	}
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
	prmRef, err := fetchASURLFromPRM(ctx, rmURL)
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

func asURLFromPRMProbe(ctx context.Context, serverURL string) (asRef, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return asRef{}, fmt.Errorf("parse server URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return asRef{}, fmt.Errorf("server URL has no scheme/host: %q", serverURL)
	}
	base := u.Scheme + "://" + u.Host
	return probePRMCandidates(ctx, base, strings.TrimRight(u.Path, "/"))
}

func prmCandidateURLs(base, path string) []string {
	candidates := []string{base + "/.well-known/oauth-protected-resource" + path}
	if path != "" {
		candidates = append(candidates, base+"/.well-known/oauth-protected-resource")
	}
	return candidates
}

func probePRMCandidates(ctx context.Context, base, path string) (asRef, error) {
	for _, c := range prmCandidateURLs(base, path) {
		ref, err := fetchASURLFromPRM(ctx, c)
		if err != nil {
			return asRef{}, err
		}
		if ref.URL != "" {
			return ref, nil
		}
	}
	return asRef{URL: base}, nil // fall back to treating the MCP server host as the AS
}

type protectedResourceMeta struct {
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

func requireHTTPURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid resource_metadata URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported protocol scheme %q in resource_metadata URL", u.Scheme)
	}
	return nil
}

func fetchASURLFromPRM(ctx context.Context, prmURL string) (asRef, error) {
	if err := requireHTTPURL(prmURL); err != nil {
		return asRef{}, err
	}
	resp, err := doDiscoveryRequest(ctx, prmURL)
	if err != nil {
		if ctx.Err() != nil {
			return asRef{}, ctx.Err()
		}
		return asRef{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return asRef{}, nil
	}
	var meta protectedResourceMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthBodyBytes)).Decode(&meta); err != nil {
		return asRef{}, nil
	}
	if len(meta.AuthorizationServers) == 0 {
		return asRef{}, nil
	}
	return asRef{URL: meta.AuthorizationServers[0], Scopes: meta.ScopesSupported}, nil
}

func discoverASMeta(ctx context.Context, asURL string) (*ServerMeta, error) {
	u, err := url.Parse(asURL)
	if err != nil {
		return nil, fmt.Errorf("parse AS URL: %w", err)
	}
	for _, candidate := range asMetaCandidates(u) {
		meta, err := fetchASMeta(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if meta != nil {
			return meta, nil
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
	AuthURL         string   `json:"authorization_endpoint"`
	TokenURL        string   `json:"token_endpoint"`
	RegistrationURL string   `json:"registration_endpoint"`
	CIMDSupported   bool     `json:"client_id_metadata_document_supported"`
	PKCEMethods     []string `json:"code_challenge_methods_supported"`
}

func fetchASMeta(ctx context.Context, metaURL string) (*ServerMeta, error) {
	resp, err := doDiscoveryRequest(ctx, metaURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
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
		return nil, fmt.Errorf("oauth discovery: authorization server %s does not support PKCE S256 (code_challenge_methods_supported=%v)", metaURL, raw.PKCEMethods)
	}
	return &ServerMeta{
		AuthURL:         raw.AuthURL,
		TokenURL:        raw.TokenURL,
		RegistrationURL: raw.RegistrationURL,
		CIMDSupported:   raw.CIMDSupported,
	}, nil
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

func doDiscoveryRequest(ctx context.Context, metaURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	return noRedirectClient.Do(req)
}

const maxAuthBodyBytes = 64 << 10

// RequiresOAuth reports whether a 401 from serverURL is confirmed (per RFC 9728) to need OAuth.
func RequiresOAuth(ctx context.Context, serverURL, wwwAuthenticate string) bool {
	scheme := strings.ToLower(strings.TrimSpace(wwwAuthenticate))
	if strings.HasPrefix(scheme, "bearer") {
		return true
	}
	if scheme != "" {
		// A non-Bearer scheme (Basic, Digest, ...) is decisive on its own — a PRM document
		// that happens to exist at the same origin doesn't apply to this specific challenge.
		return false
	}
	u, err := url.Parse(serverURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	base := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.Path, "/")
	for _, c := range prmCandidateURLs(base, path) {
		if ref, _ := fetchASURLFromPRM(ctx, c); ref.URL != "" {
			return true
		}
	}
	return false
}
