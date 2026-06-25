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

// Discover resolves OAuth endpoint metadata for an MCP server via RFC 9728 + RFC 8414.
func Discover(ctx context.Context, serverURL string) (*ServerMeta, error) {
	asURL, scopeHint, err := discoverASURL(ctx, serverURL)
	if err != nil {
		return nil, err
	}
	meta, err := discoverASMeta(ctx, asURL)
	if err != nil {
		return nil, err
	}
	// WWW-Authenticate scope takes priority over PRM scopes_supported per MCP spec §Scope Selection Strategy.
	if len(scopeHint) > 0 {
		meta.Scopes = scopeHint
	}
	return meta, nil
}

func discoverASURL(ctx context.Context, serverURL string) (asURL string, scopes []string, err error) {
	var wwwAuthScopes []string
	if asURL, wwwAuthScopes, err = asURLFromWWWAuthenticate(ctx, serverURL); err != nil || asURL != "" {
		return asURL, wwwAuthScopes, err
	}
	// Per spec, WWW-Authenticate scope takes priority even when its PRM has no authorization_servers.
	asURL, scopes, err = asURLFromPRMProbe(ctx, serverURL)
	if len(wwwAuthScopes) > 0 {
		scopes = wwwAuthScopes
	}
	return asURL, scopes, err
}

func asURLFromWWWAuthenticate(ctx context.Context, serverURL string) (string, []string, error) {
	resp, err := doDiscoveryRequest(ctx, serverURL)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil, nil
	}
	header := resp.Header.Get("WWW-Authenticate")
	rmURL := parseWWWAuthParam(header, "resource_metadata")
	if rmURL == "" {
		return "", nil, nil
	}
	// scope from WWW-Authenticate is the highest-priority hint per MCP spec §Scope Selection Strategy
	scopeHint := strings.Fields(parseWWWAuthParam(header, "scope"))
	asURL, prmScopes, err := fetchASURLFromPRM(ctx, rmURL)
	if len(scopeHint) == 0 {
		scopeHint = prmScopes
	}
	return asURL, scopeHint, err
}

// parseWWWAuthParam extracts a quoted value for the given parameter name from a WWW-Authenticate header.
func parseWWWAuthParam(header, param string) string {
	// Skip the scheme token (e.g. "Bearer ") before looking at key=value pairs.
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

func asURLFromPRMProbe(ctx context.Context, serverURL string) (string, []string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse server URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", nil, fmt.Errorf("server URL has no scheme/host: %q", serverURL)
	}
	base := u.Scheme + "://" + u.Host
	return probePRMCandidates(ctx, base, strings.TrimRight(u.Path, "/"))
}

func probePRMCandidates(ctx context.Context, base, path string) (string, []string, error) {
	candidates := []string{base + "/.well-known/oauth-protected-resource" + path}
	if path != "" {
		candidates = append(candidates, base+"/.well-known/oauth-protected-resource")
	}
	for _, c := range candidates {
		asURL, scopes, err := fetchASURLFromPRM(ctx, c)
		if err != nil {
			return "", nil, err
		}
		if asURL != "" {
			return asURL, scopes, nil
		}
	}
	return base, nil, nil // fall back to treating the MCP server host as the AS
}

type protectedResourceMeta struct {
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

func fetchASURLFromPRM(ctx context.Context, prmURL string) (string, []string, error) {
	resp, err := doDiscoveryRequest(ctx, prmURL)
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, nil
	}
	var meta protectedResourceMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthBodyBytes)).Decode(&meta); err != nil {
		return "", nil, nil
	}
	if len(meta.AuthorizationServers) == 0 {
		return "", nil, nil
	}
	return meta.AuthorizationServers[0], meta.ScopesSupported, nil
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
	ScopesSupported []string `json:"scopes_supported"`
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
	// MCP spec (2025-11-25) MUST: refuse if code_challenge_methods_supported is absent or lacks S256.
	// This check applies only to fetched AS metadata; the fallback path (no discoverable metadata)
	// proceeds without verification — mini always sends S256, so the AS will reject if unsupported.
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
