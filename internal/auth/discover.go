package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

type ServerMeta struct {
	AuthURL         string `json:"authorization_endpoint"`
	TokenURL        string `json:"token_endpoint"`
	RegistrationURL string `json:"registration_endpoint"`
}

// Discover fetches RFC 8414 metadata from the MCP server's base URL.
// Falls back to default paths (/authorize, /token, /register) on 404.
func Discover(ctx context.Context, serverURL string) (*ServerMeta, error) {
	base, err := baseURL(serverURL)
	if err != nil {
		return nil, err
	}
	meta, err := fetchMeta(ctx, base+"/.well-known/oauth-authorization-server")
	if err != nil {
		return nil, err
	}
	if meta != nil {
		return meta, nil
	}
	return fallbackMeta(base), nil
}

func fetchMeta(ctx context.Context, metaURL string) (*ServerMeta, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth discovery: unexpected status %d from %s", resp.StatusCode, metaURL)
	}
	var meta ServerMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("oauth discovery: decode metadata: %w", err)
	}
	return &meta, nil
}

func fallbackMeta(base string) *ServerMeta {
	return &ServerMeta{
		AuthURL:         base + "/authorize",
		TokenURL:        base + "/token",
		RegistrationURL: base + "/register",
	}
}

func baseURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("server URL has no scheme/host: %q", rawURL)
	}
	return u.Scheme + "://" + u.Host, nil
}
