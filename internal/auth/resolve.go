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
	if sc.Headers == nil {
		sc.Headers = make(map[string]string)
	}
	sc.Headers[sc.Auth.HeaderName()] = "Bearer " + accessToken
}

type ResolveEndpointsParams struct {
	ConfigDir  string
	ServerName string
	// Clock drives the client_secret_expires_at comparison during registration hydration.
	Clock clock.Clock
}

// ResolveEndpoints fills in missing OAuth endpoints on sc.Auth via RFC 9728
// discovery and dynamic client registration. It always canonicalizes sc.URL
// into sc.Auth.ResourceURL. Endpoint discovery is skipped when AuthURL,
// TokenURL, and ClientID are all already set.
func ResolveEndpoints(ctx context.Context, sc *config.ServerConfig, p ResolveEndpointsParams) error {
	if sc.UnsetEnv != nil {
		return sc.UnsetEnv
	}
	if err := ApplyResourceURL(sc); err != nil {
		return err
	}
	a := sc.Auth
	if a.AuthURL != "" && a.TokenURL != "" && a.ClientID != "" {
		return nil
	}
	meta, err := DiscoverAndApply(ctx, sc.URL, a)
	if err != nil {
		return err
	}
	if a.ClientID == "" {
		return resolveClientID(ctx, ClientRegParams{
			ConfigDir:  p.ConfigDir,
			ServerName: p.ServerName,
			AuthConfig: a,
			Meta:       meta,
			Now:        p.Clock.Now(),
		})
	}
	return nil
}

// DiscoverAndApply fills in a's missing OAuth endpoints from the server's discovery metadata.
func DiscoverAndApply(ctx context.Context, serverURL string, a *config.AuthConfig) (*ServerMeta, error) {
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

var endpointValidator = transport.ValidateURL

func validateEndpointURL(endpoint, name string) error {
	if endpoint == "" {
		return nil
	}
	if err := endpointValidator(endpoint); err != nil {
		return fmt.Errorf("oauth discovery: %s points to a disallowed host: %w", name, err)
	}
	return nil
}

type ClientRegParams struct {
	ConfigDir  string
	ServerName string
	AuthConfig *config.AuthConfig
	Meta       *ServerMeta
	Now        time.Time
}

func resolveClientID(ctx context.Context, p ClientRegParams) error {
	found, err := ApplyExistingClientReg(p)
	if err != nil || found {
		return err
	}
	// The spec deprecates DCR, keeping it as the fallback when CIMD can't be used.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/b488c166/docs/specification/2026-07-28/basic/authorization/client-registration.mdx?plain=1#L13-L18
	if CanUseClientMetadataDocument(p.AuthConfig, p.Meta) {
		p.AuthConfig.ClientID = ClientMetadataURL
		return nil
	}
	return dynamicRegister(ctx, p)
}

// CanUseClientMetadataDocument reports whether mini can identify itself to the server by its published
// metadata document, whose redirect URIs list only the default callback port.
func CanUseClientMetadataDocument(a *config.AuthConfig, meta *ServerMeta) bool {
	return meta != nil && meta.CIMDSupported && ResolvedCallbackPort(a) == LoopbackCallbackPort
}

func dynamicRegister(ctx context.Context, p ClientRegParams) error {
	a := p.AuthConfig
	regURL, err := registrationURL(p.Meta)
	if err != nil {
		return err
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

func registrationURL(meta *ServerMeta) (string, error) {
	switch {
	case meta != nil && meta.RegistrationURL != "":
		return meta.RegistrationURL, nil
	case meta != nil && meta.CIMDSupported:
		return "", fmt.Errorf("the server only accepts mini's client metadata document, which lists callback port %d; "+
			"remove auth.callback_port or set auth.client_id to your own app", LoopbackCallbackPort)
	}
	return "", fmt.Errorf("no client_id configured and server provides no registration endpoint")
}

// ApplyExistingClientReg reports whether a stored client registration was found and applied to p.AuthConfig.
func ApplyExistingClientReg(p ClientRegParams) (bool, error) {
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
	// RFC 7591 §3.2.1: default when omitted is client_secret_basic
	if reg.ClientSecret != "" && reg.TokenEndpointAuthMethod == "" {
		reg.TokenEndpointAuthMethod = "client_secret_basic"
	}
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
			return fmt.Errorf(
				"client registration: client_secret present but token_endpoint_auth_method is %q",
				reg.TokenEndpointAuthMethod,
			)
		}
	case "client_secret_basic", "client_secret_post":
		if reg.ClientSecret == "" {
			return fmt.Errorf(
				"client registration: token_endpoint_auth_method %q requires a client_secret",
				reg.TokenEndpointAuthMethod,
			)
		}
	default:
		return fmt.Errorf(
			"client registration: unrecognized token_endpoint_auth_method %q",
			reg.TokenEndpointAuthMethod,
		)
	}
	return nil
}
