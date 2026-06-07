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
}

// Discover resolves OAuth endpoint metadata for an MCP server via RFC 9728 + RFC 8414.
func Discover(ctx context.Context, serverURL string) (*ServerMeta, error) {
	asURL, err := discoverASURL(ctx, serverURL)
	if err != nil {
		return nil, err
	}
	return discoverASMeta(ctx, asURL)
}

func discoverASURL(ctx context.Context, serverURL string) (string, error) {
	if asURL, err := asURLFromWWWAuthenticate(ctx, serverURL); err != nil || asURL != "" {
		return asURL, err
	}
	return asURLFromPRMProbe(ctx, serverURL)
}

func asURLFromWWWAuthenticate(ctx context.Context, serverURL string) (string, error) {
	resp, err := doDiscoveryRequest(ctx, serverURL)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil
	}
	rmURL := parseResourceMetadataURL(resp.Header.Get("WWW-Authenticate"))
	if rmURL == "" {
		return "", nil
	}
	return fetchASURLFromPRM(ctx, rmURL)
}

func parseResourceMetadataURL(header string) string {
	const prefix = `resource_metadata="`
	i := strings.Index(header, prefix)
	if i == -1 {
		return ""
	}
	rest := header[i+len(prefix):]
	j := strings.Index(rest, `"`)
	if j == -1 {
		return ""
	}
	return rest[:j]
}

func asURLFromPRMProbe(ctx context.Context, serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("server URL has no scheme/host: %q", serverURL)
	}
	base := u.Scheme + "://" + u.Host
	path := strings.TrimRight(u.Path, "/")
	candidates := []string{base + "/.well-known/oauth-protected-resource" + path}
	if path != "" {
		candidates = append(candidates, base+"/.well-known/oauth-protected-resource")
	}
	for _, c := range candidates {
		asURL, err := fetchASURLFromPRM(ctx, c)
		if err != nil {
			return "", err
		}
		if asURL != "" {
			return asURL, nil
		}
	}
	return base, nil // fall back to treating the MCP server host as the AS
}

type protectedResourceMeta struct {
	AuthorizationServers []string `json:"authorization_servers"`
}

func fetchASURLFromPRM(ctx context.Context, prmURL string) (string, error) {
	resp, err := doDiscoveryRequest(ctx, prmURL)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	var meta protectedResourceMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthBodyBytes)).Decode(&meta); err != nil {
		return "", nil
	}
	if len(meta.AuthorizationServers) == 0 {
		return "", nil
	}
	return meta.AuthorizationServers[0], nil
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
	AuthURL         string `json:"authorization_endpoint"`
	TokenURL        string `json:"token_endpoint"`
	RegistrationURL string `json:"registration_endpoint"`
	CIMDSupported   bool   `json:"client_id_metadata_document_supported"`
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
	var raw rawASMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthBodyBytes)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("oauth discovery: decode metadata from %s: %w", metaURL, err)
	}
	return &ServerMeta{
		AuthURL:         raw.AuthURL,
		TokenURL:        raw.TokenURL,
		RegistrationURL: raw.RegistrationURL,
		CIMDSupported:   raw.CIMDSupported,
	}, nil
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
