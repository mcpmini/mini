package auth

import (
	"context"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func ValidateOAuthServer(serverName string, sc config.ServerConfig) error {
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		return fmt.Errorf("server %q does not have oauth2 auth configured", serverName)
	}
	return nil
}

// ApplyBearerToken sets the Authorization (or custom auth header) on sc.Headers.
func ApplyBearerToken(sc *config.ServerConfig, accessToken string) {
	headerName := sc.Auth.Header
	if headerName == "" {
		headerName = "Authorization"
	}
	if sc.Headers == nil {
		sc.Headers = make(map[string]string)
	}
	sc.Headers[headerName] = "Bearer " + accessToken
}

// ResolveConfig fills in missing OAuth endpoints (via RFC 8414 discovery) and
// client_id (via saved registration or dynamic RFC 7591 registration).
// Mutates ac in place; no-op if AuthURL, TokenURL, and ClientID are already set.
func ResolveConfig(ctx context.Context, configDir, serverName string, ac *config.AuthConfig, serverURL string) error {
	if ac.AuthURL != "" && ac.TokenURL != "" && ac.ClientID != "" {
		return nil
	}
	regURL, err := discoverEndpoints(ctx, serverURL, ac)
	if err != nil {
		return err
	}
	if ac.ClientID == "" {
		return resolveClientID(ctx, configDir, serverName, ac, regURL)
	}
	return nil
}

func discoverEndpoints(ctx context.Context, serverURL string, ac *config.AuthConfig) (string, error) {
	if ac.AuthURL != "" && ac.TokenURL != "" {
		return "", nil
	}
	meta, err := Discover(ctx, serverURL)
	if err != nil {
		return "", fmt.Errorf("discover oauth endpoints: %w", err)
	}
	if err := fillEndpoints(ac, meta); err != nil {
		return "", err
	}
	return meta.RegistrationURL, nil
}

func fillEndpoints(ac *config.AuthConfig, meta *ServerMeta) error {
	if ac.AuthURL == "" {
		if err := validateEndpointURL(meta.AuthURL, "authorization_endpoint"); err != nil {
			return err
		}
		ac.AuthURL = meta.AuthURL
	}
	if ac.TokenURL == "" {
		if err := validateEndpointURL(meta.TokenURL, "token_endpoint"); err != nil {
			return err
		}
		ac.TokenURL = meta.TokenURL
	}
	return validateEndpointURL(meta.RegistrationURL, "registration_endpoint")
}

func validateEndpointURL(endpoint, name string) error {
	if endpoint == "" {
		return nil
	}
	if err := transport.ValidateURL(endpoint); err != nil {
		return fmt.Errorf("oauth discovery: %s points to a disallowed host: %w", name, err)
	}
	return nil
}

func resolveClientID(ctx context.Context, configDir, serverName string, ac *config.AuthConfig, regURL string) error {
	reg, err := LoadRegistration(configDir, serverName)
	if err == nil {
		ac.ClientID = reg.ClientID
		return nil
	}
	if !IsNotFound(err) {
		return err
	}
	if regURL == "" {
		return fmt.Errorf("no client_id configured and server provides no registration endpoint")
	}
	clientID, err := Register(ctx, regURL)
	if err != nil {
		return err
	}
	ac.ClientID = clientID
	return SaveRegistration(configDir, serverName, &Registration{ClientID: clientID})
}
