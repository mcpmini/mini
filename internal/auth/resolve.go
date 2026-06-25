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

// ResolveEndpoints fills in missing OAuth endpoints on sc.Auth via RFC 9728
// discovery and dynamic client registration, and sets sc.Auth.ResourceURL from
// sc.URL. It is a no-op if AuthURL, TokenURL, and ClientID are all already set.
func ResolveEndpoints(ctx context.Context, configDir, serverName string, sc *config.ServerConfig) error {
	a := sc.Auth
	a.ResourceURL = sc.URL
	if a.AuthURL != "" && a.TokenURL != "" && a.ClientID != "" {
		return nil
	}
	meta, err := discoverAndApply(ctx, sc.URL, a)
	if err != nil {
		return err
	}
	if a.ClientID == "" {
		return resolveClientID(ctx, clientRegParams{ConfigDir: configDir, ServerName: serverName, AuthConfig: a, Meta: meta})
	}
	return nil
}

func discoverAndApply(ctx context.Context, serverURL string, a *config.AuthConfig) (*ServerMeta, error) {
	if a.AuthURL != "" && a.TokenURL != "" && a.ClientID != "" {
		return nil, nil
	}
	meta, err := Discover(ctx, serverURL)
	if err != nil {
		return nil, fmt.Errorf("discover oauth endpoints: %w", err)
	}
	return meta, applyDiscoveredEndpoints(a, meta)
}

func applyDiscoveredEndpoints(a *config.AuthConfig, meta *ServerMeta) error {
	if a.AuthURL == "" {
		if err := validateEndpointURL(meta.AuthURL, "authorization_endpoint"); err != nil {
			return err
		}
		a.AuthURL = meta.AuthURL
	}
	if a.TokenURL == "" {
		if err := validateEndpointURL(meta.TokenURL, "token_endpoint"); err != nil {
			return err
		}
		a.TokenURL = meta.TokenURL
	}
	if len(a.Scopes) == 0 && len(meta.Scopes) > 0 {
		a.Scopes = meta.Scopes
	}
	return nil
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

type clientRegParams struct {
	ConfigDir  string
	ServerName string
	AuthConfig *config.AuthConfig
	Meta       *ServerMeta
}

func resolveClientID(ctx context.Context, p clientRegParams) error {
	// Cached registration takes priority over CIMD per MCP spec §Client Registration Approaches.
	// Servers that advertise CIMD but reject arbitrary metadata URLs (e.g. Linear) rely on this.
	found, err := applyExistingClientReg(p.ConfigDir, p.ServerName, p.AuthConfig)
	if err != nil || found {
		return err
	}
	if p.Meta != nil && p.Meta.CIMDSupported {
		p.AuthConfig.ClientID = ClientMetadataURL
		return nil
	}
	return dynamicRegister(ctx, p)
}

func dynamicRegister(ctx context.Context, p clientRegParams) error {
	a, configDir, serverName, meta := p.AuthConfig, p.ConfigDir, p.ServerName, p.Meta
	regURL := ""
	if meta != nil {
		regURL = meta.RegistrationURL
	}
	if regURL == "" {
		return fmt.Errorf("no client_id configured and server provides no registration endpoint")
	}
	if err := validateEndpointURL(regURL, "registration_endpoint"); err != nil {
		return err
	}
	clientID, err := Register(ctx, regURL, ResolvedCallbackURI(a))
	if err != nil {
		return err
	}
	a.ClientID = clientID
	return SaveRegistration(configDir, serverName, &Registration{ClientID: clientID})
}

func applyExistingClientReg(configDir, serverName string, a *config.AuthConfig) (bool, error) {
	reg, err := LoadRegistration(configDir, serverName)
	if err == nil {
		a.ClientID = reg.ClientID
		return true, nil
	}
	if !IsNotFound(err) {
		return false, err
	}
	return false, nil
}
