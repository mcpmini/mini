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
	_, _, err := ResolveEndpointsWithClient(ctx, sc, p)
	return err
}

func ResolveEndpointsWithClient(
	ctx context.Context,
	sc *config.ServerConfig,
	p ResolveEndpointsParams,
) (Client, *ServerMeta, error) {
	if p.Clock == nil {
		p.Clock = clock.System()
	}
	if err := prepareLoginAuth(sc); err != nil {
		return Client{}, nil, err
	}
	a := sc.Auth
	configuredClientID := a.ClientID
	if a.AuthURL != "" && a.TokenURL != "" && a.ClientID != "" {
		return Client{Source: ClientConfigured, ID: a.ClientID}, nil, nil
	}
	return resolveDiscoveredClient(discoveredClientParams{
		ctx: ctx, serverConfig: sc, resolve: p, configuredClientID: configuredClientID,
	})
}

type discoveredClientParams struct {
	ctx                context.Context
	serverConfig       *config.ServerConfig
	resolve            ResolveEndpointsParams
	configuredClientID string
}

func resolveDiscoveredClient(p discoveredClientParams) (Client, *ServerMeta, error) {
	ctx, sc, resolveParams := p.ctx, p.serverConfig, p.resolve
	a := sc.Auth
	meta, err := DiscoverAndApply(ctx, sc.URL, a)
	if err != nil {
		return Client{}, nil, err
	}
	client, err := resolveClientID(ctx, ClientRegParams{
		ConfigDir:  resolveParams.ConfigDir,
		ServerName: resolveParams.ServerName,
		AuthConfig: a,
		Meta:       meta,
		Now:        resolveParams.Clock.Now(),
	})
	if err != nil {
		return Client{}, nil, err
	}
	if p.configuredClientID != "" {
		client = Client{Source: ClientConfigured, ID: p.configuredClientID}
	}
	return client, meta, nil
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

func resolveClientID(ctx context.Context, p ClientRegParams) (Client, error) {
	var reg *Registration
	if p.AuthConfig.ClientID == "" {
		var err error
		reg, err = LoadRegistration(p.ConfigDir, p.ServerName)
		if err != nil && !IsNotFound(err) {
			return Client{}, err
		}
	}
	choice, err := SelectClient(ClientSelectionParams{
		AuthConfig: p.AuthConfig, Registration: reg, Meta: p.Meta, Now: p.Now,
	})
	if err != nil {
		return Client{}, err
	}
	if choice.Register {
		return dynamicRegister(ctx, p)
	}
	if choice.Client.Source == ClientRegistered {
		if err := applyRegistration(p.AuthConfig, reg, p.Now); err != nil {
			return Client{}, err
		}
	}
	if choice.Client.Source == ClientMetadataDocument {
		p.AuthConfig.ClientID = choice.Client.ID
	}
	return choice.Client, nil
}

type ClientSelectionParams struct {
	AuthConfig   *config.AuthConfig
	Registration *Registration
	Meta         *ServerMeta
	Now          time.Time
}

type ClientChoice struct {
	Client   Client
	Register bool
}

func SelectClient(p ClientSelectionParams) (ClientChoice, error) {
	a := p.AuthConfig
	if a.ClientID != "" {
		return ClientChoice{Client: Client{Source: ClientConfigured, ID: a.ClientID}}, nil
	}
	if choice, selected, err := selectStoredRegistration(p); err != nil || selected {
		return choice, err
	}
	return selectNewClient(p)
}

func selectStoredRegistration(p ClientSelectionParams) (ClientChoice, bool, error) {
	reg, meta, now := p.Registration, p.Meta, p.Now
	if reg == nil || meta == nil || meta.Issuer == "" || reg.Issuer != meta.Issuer || reg.ClientID == "" {
		return ClientChoice{}, false, nil
	}
	selectedReg := registrationWithDefaultAuthMethod(reg)
	if err := validateRegistrationConsistency(selectedReg); err != nil {
		return ClientChoice{}, true, err
	}
	if reg.ClientSecret != "" && !secretApplies(reg, now) {
		return ClientChoice{}, false, nil
	}
	if p.AuthConfig.TokenURL != "" && p.AuthConfig.TokenURL != meta.TokenURL {
		return ClientChoice{}, true, fmt.Errorf(
			"configured token_url does not match the registered client's authorization server; configure your own client",
		)
	}
	return ClientChoice{Client: registrationClient(selectedReg, now)}, true, nil
}

func selectNewClient(p ClientSelectionParams) (ClientChoice, error) {
	a, meta := p.AuthConfig, p.Meta
	if meta == nil || meta.Issuer == "" {
		return ClientChoice{}, fmt.Errorf(
			"no configured OAuth client and no authorization server metadata; configure your own client",
		)
	}
	if CanUseClientMetadataDocument(a, meta) {
		return ClientChoice{Client: Client{Source: ClientMetadataDocument, ID: ClientMetadataURL}}, nil
	}
	if meta.RegistrationURL != "" {
		if a.TokenURL != "" && a.TokenURL != meta.TokenURL {
			return ClientChoice{}, fmt.Errorf(
				"configured token_url does not match the authorization server; configure your own client",
			)
		}
		return ClientChoice{Register: true}, nil
	}
	if meta.CIMDSupported {
		return ClientChoice{}, fmt.Errorf(
			"the server only accepts mini's client metadata document, which lists callback port %d; "+
				"remove auth.callback_port or set auth.client_id to your own app",
			LoopbackCallbackPort,
		)
	}
	return ClientChoice{}, fmt.Errorf(
		"the authorization server does not support client metadata documents or dynamic registration; " +
			"configure your own client",
	)
}

func registrationWithDefaultAuthMethod(reg *Registration) *Registration {
	copy := *reg
	if copy.ClientSecret != "" && copy.TokenEndpointAuthMethod == "" {
		copy.TokenEndpointAuthMethod = "client_secret_basic"
	}
	return &copy
}

func registrationClient(reg *Registration, now time.Time) Client {
	client := Client{Source: ClientRegistered, ID: reg.ClientID}
	client.AuthMethod = reg.TokenEndpointAuthMethod
	if client.AuthMethod == "" && reg.ClientSecret != "" {
		client.AuthMethod = "client_secret_basic"
	}
	if secretApplies(reg, now) {
		client.Secret = reg.ClientSecret
	}
	return client
}

// CanUseClientMetadataDocument reports whether mini can identify itself to the server by its published
// metadata document, whose redirect URIs list only the default callback port.
func CanUseClientMetadataDocument(a *config.AuthConfig, meta *ServerMeta) bool {
	return meta != nil && meta.CIMDSupported && ResolvedCallbackPort(a) == LoopbackCallbackPort
}

func dynamicRegister(ctx context.Context, p ClientRegParams) (Client, error) {
	a := p.AuthConfig
	regURL, err := registrationURL(p.Meta)
	if err != nil {
		return Client{}, err
	}
	result, err := Register(ctx, regURL, ResolvedCallbackURI(a))
	if err != nil {
		return Client{}, err
	}
	reg := &Registration{
		Issuer:                  p.Meta.Issuer,
		ClientID:                result.ClientID,
		ClientSecret:            result.ClientSecret,
		TokenEndpointAuthMethod: result.TokenEndpointAuthMethod,
		ClientSecretExpiresAt:   result.ClientSecretExpiresAt,
	}
	if err := applyRegistration(a, reg, p.Now); err != nil {
		return Client{}, err
	}
	if err := SaveRegistration(p.ConfigDir, p.ServerName, reg); err != nil {
		return Client{}, err
	}
	return registrationClient(reg, p.Now), nil
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
	case "":
		return nil
	case "none":
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

func prepareLoginAuth(sc *config.ServerConfig) error {
	if sc.UnsetEnv != nil {
		return sc.UnsetEnv
	}
	resource, err := CanonicalResourceURI(sc.URL)
	if err != nil {
		return err
	}
	a := sc.Auth
	if a.ClientID == "" {
		a.ClientSecret = ""
		a.TokenEndpointAuthMethod = ""
	}
	a.ResourceURL = resource
	return nil
}
