package auth

import (
	"errors"
	"fmt"
	"net/url"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
)

// Login is a token with the server, token endpoint and client it was issued for.
type Login struct {
	Resource string        `json:"resource"`
	TokenURL string        `json:"token_url"`
	Client   LoginClient   `json:"client"`
	Token    *oauth2.Token `json:"token"`
}

// LoginClient is the OAuth client a login was issued to.
type LoginClient struct {
	ID string `json:"id"`
	// Configured clients keep their secret and auth method in the server config, so editing them
	// there takes effect; a registered client's are kept here because no config holds them.
	Configured bool   `json:"configured,omitempty"`
	Secret     string `json:"secret,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
}

func (l *Login) validate() error {
	if l.Resource == "" || l.TokenURL == "" || l.Client.ID == "" || l.Token == nil {
		return errors.New("login record is missing its resource, token_url, client or token")
	}
	endpoint, err := url.Parse(l.TokenURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return errors.New("login record token_url is not an absolute HTTP URL")
	}
	if l.Client.Configured && (l.Client.Secret != "" || l.Client.AuthMethod != "") {
		return errors.New("login record stores credentials for a configured client")
	}
	return nil
}

// MatchesConfig returns why the login can't be used with sc, or nil when it can.
func (l *Login) MatchesConfig(sc config.ServerConfig) error {
	resource, err := CanonicalResourceURI(sc.URL)
	if err != nil || resource != l.Resource {
		return errors.New("the server URL changed")
	}
	configuredID, configuredTokenURL := "", ""
	if sc.Auth != nil {
		configuredID, configuredTokenURL = sc.Auth.ClientID, sc.Auth.TokenURL
	}
	if configuredID != l.Client.ID && (configuredID != "" || l.Client.Configured) {
		return errors.New("the client_id changed")
	}
	if configuredTokenURL != "" && configuredTokenURL != l.TokenURL {
		return errors.New("the token_url changed")
	}
	return nil
}

// LoadLoginFor loads the saved login for sc, refusing one made for other settings.
func LoadLoginFor(configDir string, sc config.ServerConfig) (*Login, error) {
	login, err := LoadLogin(configDir, sc.Name)
	if err != nil {
		return nil, err
	}
	if err := login.MatchesConfig(sc); err != nil {
		return nil, mismatchedLogin(err)
	}
	return login, nil
}

func mismatchedLogin(cause error) error {
	return fmt.Errorf("the login was made for other settings (%w)", cause)
}

// RefreshConfig is configured with the endpoint and client the login was issued for, never anything
// rediscovered from the current server URL.
func (l *Login) RefreshConfig(configured *config.AuthConfig) *config.AuthConfig {
	ac := configured.Clone()
	ac.TokenURL = l.TokenURL
	ac.ResourceURL = l.Resource
	ac.ClientID = l.Client.ID
	if !l.Client.Configured {
		ac.ClientSecret = l.Client.Secret
		ac.TokenEndpointAuthMethod = l.Client.AuthMethod
	}
	return ac
}
