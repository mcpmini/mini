package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// LoopbackCallbackPath is the redirect URI path used for all OAuth callback
// listeners. Registration omits the port (per RFC 8252 §7.3, the AS MUST allow
// any port for loopback redirect URIs at request time). Non-RFC-8252-compliant
// servers that do exact URI matching will reject the port mismatch; users of
// those servers must pre-configure a client_id in their server YAML.
const LoopbackCallbackPath = "/callback"

// loopbackRedirectBase is registered without a port. RFC 8252 §7.3 says:
// "The authorization server MUST allow any port to be specified at the time of
// the request for loopback IP redirect URIs." The actual PKCE flow appends an
// ephemeral port, which compliant servers accept.
const loopbackRedirectBase = "http://127.0.0.1"

// Register performs RFC 7591 dynamic client registration and returns the client_id.
func Register(ctx context.Context, registrationURL string) (string, error) {
	body, _ := json.Marshal(registrationRequest{
		ClientName:              "mini",
		RedirectURIs:            []string{loopbackRedirectBase + LoopbackCallbackPath},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	})
	resp, err := postRegistration(ctx, registrationURL, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parseClientID(resp, registrationURL)
}

func postRegistration(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return noRedirectClient.Do(req)
}

func parseClientID(resp *http.Response, url string) (string, error) {
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("client registration: status %d from %s", resp.StatusCode, url)
	}
	var result struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthBodyBytes)).Decode(&result); err != nil {
		return "", fmt.Errorf("client registration: decode response: %w", err)
	}
	if result.ClientID == "" {
		return "", fmt.Errorf("client registration: server returned empty client_id")
	}
	return result.ClientID, nil
}
