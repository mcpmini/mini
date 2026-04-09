package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// Register performs RFC 7591 dynamic client registration and returns the client_id.
func Register(ctx context.Context, registrationURL string) (string, error) {
	body, _ := json.Marshal(registrationRequest{
		ClientName:              "mini",
		RedirectURIs:            []string{"http://127.0.0.1/callback"},
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
	return http.DefaultClient.Do(req)
}

func parseClientID(resp *http.Response, url string) (string, error) {
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("client registration: status %d from %s", resp.StatusCode, url)
	}
	var result struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("client registration: decode response: %w", err)
	}
	if result.ClientID == "" {
		return "", fmt.Errorf("client registration: server returned empty client_id")
	}
	return result.ClientID, nil
}
