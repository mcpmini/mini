package auth

import (
	"context"
	"fmt"
	"net/url"
	"time"

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

type resolvedLogin struct {
	Client    LoginClient
	Issuer    ExpectedIssuer
	flow      *oauthHTTPFlow
	ServerURL string
}

func resolveEndpoints(ctx context.Context, sc *config.ServerConfig, p BeginLoginParams) (resolvedLogin, error) {
	if sc.UnsetEnv != nil {
		return resolvedLogin{}, sc.UnsetEnv
	}
	flow := newOAuthHTTPFlow(ctx, *sc)
	resolved, err := flow.resolveLogin(ctx, sc, p)
	if err != nil {
		flow.closeIdleConnections()
	}
	return resolved, err
}

func (f *oauthHTTPFlow) resolveLogin(
	ctx context.Context,
	sc *config.ServerConfig,
	p BeginLoginParams,
) (resolvedLogin, error) {
	serverURL, err := CanonicalResourceURI(sc.URL)
	if err != nil {
		return resolvedLogin{}, err
	}
	a := sc.Auth
	resolved := resolvedLogin{ServerURL: serverURL, flow: f}
	if a.ClientID == "" {
		// A configured secret belongs to a configured client; never send it with a registered or CIMD id.
		a.ClientSecret, a.TokenEndpointAuthMethod = "", ""
	}
	meta, issuer, err := f.discoverForLogin(ctx, sc.URL, a)
	if err != nil {
		return resolvedLogin{}, err
	}
	resolved.Issuer = issuer
	resolved.Client, err = resolveClient(ctx, ClientRegParams{
		ConfigDir: p.ConfigDir, ServerName: p.ServerName, AuthConfig: a, Meta: meta, Now: p.Clock.Now(), flow: f,
	})
	return resolved, err
}

func (f *oauthHTTPFlow) discoverForLogin(
	ctx context.Context,
	serverURL string,
	a *config.AuthConfig,
) (*ServerMeta, ExpectedIssuer, error) {
	meta, err := f.discoverAndApply(ctx, serverURL, a)
	if err != nil {
		return nil, ExpectedIssuer{}, err
	}
	if meta.Issuer == "" {
		return nil, ExpectedIssuer{}, fmt.Errorf("authorization server metadata is missing its issuer")
	}
	return meta, ExpectedIssuer{Issuer: meta.Issuer, AlwaysSent: meta.IssParameterSupported}, nil
}

func validateConfiguredIssuer(a *config.AuthConfig, meta *ServerMeta) error {
	issuer := meta.Issuer
	if a.Issuer == issuer {
		return nil
	}
	if a.Issuer == "" && meta.CIMDSupported && portableClientID(a) {
		return nil
	}
	if a.Issuer == "" {
		return fmt.Errorf(
			"configured OAuth client requires auth.issuer identifying the server that registered it; discovery reports %q",
			issuer,
		)
	}
	return fmt.Errorf(
		"configured OAuth client belongs to issuer %q, but the server now uses %q; configure a client registered with that issuer",
		a.Issuer,
		issuer,
	)
}

func portableClientID(a *config.AuthConfig) bool {
	u, err := url.Parse(a.ClientID)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Path != "" && u.User == nil && u.Fragment == "" &&
		a.ClientSecret == "" &&
		(a.TokenEndpointAuthMethod == "" || a.TokenEndpointAuthMethod == "none")
}

func (f *oauthHTTPFlow) discoverAndApply(
	ctx context.Context,
	serverURL string,
	a *config.AuthConfig,
) (*ServerMeta, error) {
	meta, err := f.discover(ctx, serverURL)
	if err != nil {
		return nil, fmt.Errorf("discover oauth endpoints: %w", err)
	}
	a.ResourceURL = meta.Resource
	return meta, f.applyDiscoveredEndpoints(a, meta)
}

func (f *oauthHTTPFlow) applyDiscoveredEndpoints(a *config.AuthConfig, meta *ServerMeta) error {
	if a.AuthURL == "" {
		if err := f.validateDiscoveredEndpoint(meta.AuthURL, "authorization_endpoint"); err != nil {
			return err
		}
		a.AuthURL = meta.AuthURL
	} else if err := validateConfiguredEndpoint(a.AuthURL, "authorization_endpoint"); err != nil {
		return err
	}
	if a.TokenURL == "" {
		if err := f.validateDiscoveredEndpoint(meta.TokenURL, "token_endpoint"); err != nil {
			return err
		}
		a.TokenURL = meta.TokenURL
	} else if err := validateConfiguredEndpoint(a.TokenURL, "token_endpoint"); err != nil {
		return err
	}
	if len(a.Scopes) == 0 && len(meta.Scopes) > 0 {
		a.Scopes = meta.Scopes
	}
	return nil
}

var endpointValidator = transport.ValidateURL

func validateConfiguredEndpoint(endpoint, name string) error {
	if endpoint == "" {
		return nil
	}
	if err := requireHTTPURL(endpoint); err != nil {
		return fmt.Errorf("oauth %s: %w", name, err)
	}
	return nil
}

func (f *oauthHTTPFlow) validateDiscoveredEndpoint(endpoint, name string) error {
	if err := validateConfiguredEndpoint(endpoint, name); err != nil {
		return err
	}
	if endpoint == "" {
		return nil
	}
	validate := endpointValidator
	if f.privateReady() {
		validate = (transport.SSRFPolicy{AllowPrivateNetworks: true}).ValidateURL
	}
	if err := validate(endpoint); err != nil {
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
	flow       *oauthHTTPFlow
}

func resolveClient(ctx context.Context, p ClientRegParams) (LoginClient, error) {
	if p.AuthConfig.ClientID != "" {
		if err := validateConfiguredIssuer(p.AuthConfig, p.Meta); err != nil {
			return LoginClient{}, err
		}
		return LoginClient{ID: p.AuthConfig.ClientID, Configured: true}, nil
	}
	reg, err := LoadRegistration(p.ConfigDir, p.ServerName)
	if err != nil && !IsNotFound(err) {
		return LoginClient{}, err
	}
	if err == nil && registrationUsable(reg, p) {
		return applyRegistration(p.AuthConfig, reg)
	}
	// The spec deprecates DCR, keeping it as the fallback when CIMD can't be used.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/b488c166/docs/specification/2026-07-28/basic/authorization/client-registration.mdx?plain=1#L13-L18
	if CanUseClientMetadataDocument(p.AuthConfig, p.Meta) {
		p.AuthConfig.ClientID = ClientMetadataURL
		return LoginClient{ID: ClientMetadataURL}, nil
	}
	return dynamicRegister(ctx, p)
}

// registrationUsable rejects a client registered with another authorization server, which wouldn't
// know it and mustn't receive its secret, and a client whose secret expired, which can't log in.
func registrationUsable(reg *Registration, p ClientRegParams) bool {
	if p.Meta == nil || reg.Issuer == "" || reg.Issuer != p.Meta.Issuer || reg.TokenURL != p.AuthConfig.TokenURL {
		return false
	}
	return reg.ClientSecret == "" || secretApplies(reg, p.Now)
}

// CanUseClientMetadataDocument reports whether mini can identify itself to the server by its published
// metadata document, whose redirect URIs list only the default callback port.
func CanUseClientMetadataDocument(a *config.AuthConfig, meta *ServerMeta) bool {
	return meta != nil && meta.CIMDSupported && ResolvedCallbackPort(a) == LoopbackCallbackPort
}

func dynamicRegister(ctx context.Context, p ClientRegParams) (LoginClient, error) {
	a := p.AuthConfig
	regURL, err := registrationURL(p.Meta)
	if err != nil {
		return LoginClient{}, err
	}
	result, err := p.flow.register(ctx, regURL, ResolvedCallbackURI(a))
	if err != nil {
		return LoginClient{}, err
	}
	reg := &Registration{
		Issuer:                  p.Meta.Issuer,
		ClientID:                result.ClientID,
		ClientSecret:            result.ClientSecret,
		TokenEndpointAuthMethod: result.TokenEndpointAuthMethod,
		ClientSecretExpiresAt:   result.ClientSecretExpiresAt,
		TokenURL:                a.TokenURL,
	}
	client, err := applyRegistration(a, reg)
	if err != nil {
		return LoginClient{}, err
	}
	return client, SaveRegistration(p.ConfigDir, p.ServerName, reg)
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

func applyRegistration(a *config.AuthConfig, reg *Registration) (LoginClient, error) {
	// RFC 7591 §3.2.1: default when omitted is client_secret_basic
	if reg.ClientSecret != "" && reg.TokenEndpointAuthMethod == "" {
		reg.TokenEndpointAuthMethod = "client_secret_basic"
	}
	if err := validateRegistrationConsistency(reg); err != nil {
		return LoginClient{}, err
	}
	a.ClientID = reg.ClientID
	a.ClientSecret = reg.ClientSecret
	a.TokenEndpointAuthMethod = reg.TokenEndpointAuthMethod
	return LoginClient{ID: reg.ClientID, Secret: reg.ClientSecret, AuthMethod: reg.TokenEndpointAuthMethod}, nil
}

func secretApplies(reg *Registration, now time.Time) bool {
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
