package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func ValidateOAuthServer(serverName string, sc config.ServerConfig) error {
	if sc.Auth == nil || sc.Auth.Type != config.AuthTypeOAuth2 {
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

type ResolveEndpointsParams struct {
	ConfigDir  string
	ServerName string
	// Clock drives the client_secret_expires_at comparison during registration hydration.
	Clock clock.Clock
}

// ResolveEndpoints fills in missing OAuth endpoints on sc.Auth via RFC 9728
// discovery and dynamic client registration, and sets sc.Auth.ResourceURL from
// sc.URL. It is a no-op if AuthURL, TokenURL, and ClientID are all already set.
func ResolveEndpoints(ctx context.Context, sc *config.ServerConfig, p ResolveEndpointsParams) error {
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
		return resolveClientID(ctx, clientRegParams{
			ConfigDir:  p.ConfigDir,
			ServerName: p.ServerName,
			AuthConfig: a,
			Meta:       meta,
			Now:        p.Clock.Now(),
		})
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
	Now        time.Time
}

func resolveClientID(ctx context.Context, p clientRegParams) error {
	found, err := applyExistingClientReg(p)
	if err != nil || found {
		return err
	}
	// DCR before CIMD: servers like Linear advertise CIMD but only accept pre-approved metadata URLs,
	// rejecting ours with "Invalid client". MCP spec §Client Registration Approaches uses SHOULD.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/977e7481/docs/specification/2025-11-25/basic/authorization.mdx?plain=1#L204-L208
	if p.Meta != nil && p.Meta.RegistrationURL != "" {
		return dynamicRegister(ctx, p)
	}
	if p.Meta != nil && p.Meta.CIMDSupported {
		p.AuthConfig.ClientID = ClientMetadataURL
		return nil
	}
	return dynamicRegister(ctx, p)
}

func dynamicRegister(ctx context.Context, p clientRegParams) error {
	a, meta := p.AuthConfig, p.Meta
	regURL := ""
	if meta != nil {
		regURL = meta.RegistrationURL
	}
	if regURL == "" {
		return fmt.Errorf("no client_id configured and server provides no registration endpoint")
	}
	result, err := Register(ctx, regURL, ResolvedCallbackURI(a))
	if err != nil {
		return err
	}
	reg := &Registration{
		ClientID:                result.ClientID,
		ClientSecret:            result.ClientSecret,
		TokenEndpointAuthMethod: result.TokenEndpointAuthMethod,
		ClientSecretExpiresAt:   result.ClientSecretExpiresAt,
	}
	if err := applyRegistration(a, reg, p.Now); err != nil {
		return err
	}
	return SaveRegistration(p.ConfigDir, p.ServerName, reg)
}

func applyExistingClientReg(p clientRegParams) (bool, error) {
	reg, err := LoadRegistration(p.ConfigDir, p.ServerName)
	if err == nil {
		return true, applyRegistration(p.AuthConfig, reg, p.Now)
	}
	if !IsNotFound(err) {
		return false, err
	}
	return false, nil
}

func applyRegistration(a *config.AuthConfig, reg *Registration, now time.Time) error {
	a.ClientID = reg.ClientID
	// reject rather than silently use a wrong auth style
	if err := validateRegistrationConsistency(reg); err != nil {
		return err
	}
	// treat as absent — token exchange surfaces the AS's own error instead of silent re-registration
	if !secretApplies(reg, now) {
		return nil
	}
	a.ClientSecret = reg.ClientSecret
	a.TokenEndpointAuthMethod = reg.TokenEndpointAuthMethod
	return nil
}

func secretApplies(reg *Registration, now time.Time) bool {
	if reg.ClientSecret == "" {
		return false
	}
	if reg.ClientSecretExpiresAt == 0 {
		return true
	}
	return time.Unix(reg.ClientSecretExpiresAt, 0).After(now)
}

func validateRegistrationConsistency(reg *Registration) error {
	switch reg.TokenEndpointAuthMethod {
	case "", "none":
		if reg.ClientSecret != "" {
			return fmt.Errorf("client registration: client_secret present but token_endpoint_auth_method is %q", reg.TokenEndpointAuthMethod)
		}
	case "client_secret_basic", "client_secret_post":
		if reg.ClientSecret == "" {
			return fmt.Errorf("client registration: token_endpoint_auth_method %q requires a client_secret", reg.TokenEndpointAuthMethod)
		}
	default:
		return fmt.Errorf("client registration: unrecognized token_endpoint_auth_method %q", reg.TokenEndpointAuthMethod)
	}
	return nil
}
