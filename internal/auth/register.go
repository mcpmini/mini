package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/mcpmini/mini/internal/config"
)

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// LoopbackCallbackPath is the redirect URI path used for all OAuth callback listeners.
const LoopbackCallbackPath = "/callback"

// LoopbackCallbackPort is fixed (not random) because servers like Atlassian exact-match
// redirect URIs — DCR must register the same URI the PKCE flow sends. 6464 = MINI.
const LoopbackCallbackPort = 6464

// ResolvedCallbackPort returns the configured callback port, or LoopbackCallbackPort if not set.
func ResolvedCallbackPort(ac *config.AuthConfig) int {
	if ac != nil && ac.CallbackPort != 0 {
		return ac.CallbackPort
	}
	return LoopbackCallbackPort
}

// Used by both DCR registration and the PKCE flow so they always register and send the same URI.
func ResolvedCallbackURI(ac *config.AuthConfig) string {
	return fmt.Sprintf("http://localhost:%d%s", ResolvedCallbackPort(ac), LoopbackCallbackPath)
}

// Register performs RFC 7591 dynamic client registration and returns the client_id.
func Register(ctx context.Context, registrationURL, callbackURI string) (string, error) {
	body, _ := json.Marshal(registrationRequest{
		ClientName:              "mini",
		RedirectURIs:            []string{callbackURI},
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
