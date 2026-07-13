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

// RegistrationResult carries whatever the server actually assigned — RFC 7591
// §3.2.1 lets it override the requested public-client metadata, e.g. by
// registering mini as confidential and returning a client_secret.
type RegistrationResult struct {
	ClientID                string
	ClientSecret            string
	TokenEndpointAuthMethod string
	ClientSecretExpiresAt   int64
}

// Register performs RFC 7591 dynamic client registration, requesting
// token_endpoint_auth_method "none" (public client with PKCE).
func Register(ctx context.Context, registrationURL, callbackURI string) (RegistrationResult, error) {
	body, _ := json.Marshal(registrationRequest{
		ClientName:              "mini",
		RedirectURIs:            []string{callbackURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	})
	resp, err := postRegistration(ctx, registrationURL, body)
	if err != nil {
		return RegistrationResult{}, err
	}
	defer resp.Body.Close()
	return parseRegistrationResponse(resp, registrationURL)
}

func postRegistration(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return noRedirectClient.Do(req)
}

func parseRegistrationResponse(resp *http.Response, url string) (RegistrationResult, error) {
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return RegistrationResult{}, fmt.Errorf("client registration: status %d from %s", resp.StatusCode, url)
	}
	var result struct {
		ClientID                string `json:"client_id"`
		ClientSecret            string `json:"client_secret"`
		TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
		ClientSecretExpiresAt   int64  `json:"client_secret_expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthBodyBytes)).Decode(&result); err != nil {
		return RegistrationResult{}, fmt.Errorf("client registration: decode response: %w", err)
	}
	if result.ClientID == "" {
		return RegistrationResult{}, fmt.Errorf("client registration: server returned empty client_id")
	}
	return RegistrationResult{
		ClientID:                result.ClientID,
		ClientSecret:            result.ClientSecret,
		TokenEndpointAuthMethod: result.TokenEndpointAuthMethod,
		ClientSecretExpiresAt:   result.ClientSecretExpiresAt,
	}, nil
}
