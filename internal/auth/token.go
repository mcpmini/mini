package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/fileio"
)

type ClientSource string

const (
	ClientConfigured       ClientSource = "configured"
	ClientRegistered       ClientSource = "registered"
	ClientMetadataDocument ClientSource = "metadata_document"
)

type Client struct {
	Source     ClientSource `json:"source"`
	ID         string       `json:"id"`
	Secret     string       `json:"secret,omitempty"`
	AuthMethod string       `json:"auth_method,omitempty"`
}

type Login struct {
	Resource string        `json:"resource"`
	TokenURL string        `json:"token_url"`
	Client   Client        `json:"client"`
	Token    *oauth2.Token `json:"token"`
}

type LoginState int

const (
	Missing LoginState = iota + 1
	Unreadable
	ForOtherSettings
	Expired
	Refreshable
	Valid
)

type LoginSnapshot struct {
	Login  *Login
	State  LoginState
	Reason string
}

type LoginEvaluation struct {
	Login   *Login
	LoadErr error
	Server  config.ServerConfig
	Now     time.Time
}

func (s LoginState) NeedsLogin() bool { return s != Valid && s != Refreshable }

func (s LoginState) Remedy(server string) string {
	if s == ForOtherSettings || s == Unreadable || s == Missing {
		return "run mini auth " + server
	}
	if s == Expired {
		return "run mini auth " + server
	}
	return ""
}

func ValidateLogin(login *Login) error {
	if login == nil || login.Resource == "" || login.TokenURL == "" || login.Token == nil {
		return errors.New("login record is missing required resource, token_url, or token")
	}
	endpoint, err := url.ParseRequestURI(login.TokenURL)
	if err != nil {
		return fmt.Errorf("login record token_url is invalid: %w", err)
	}
	if endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return errors.New("login record token_url is not an absolute HTTP URL")
	}
	resource, err := CanonicalResourceURI(login.Resource)
	if err != nil || resource != login.Resource {
		return errors.New("login record resource is not a canonical resource URI")
	}
	return validateLoginClient(login.Client)
}

func validateLoginClient(client Client) error {
	if client.ID == "" {
		return errors.New("login record is missing client ID")
	}
	switch client.Source {
	case ClientConfigured, ClientMetadataDocument:
		if client.Secret != "" || client.AuthMethod != "" {
			return fmt.Errorf("login record source %q cannot contain client credentials", client.Source)
		}
	case ClientRegistered:
		if client.Secret != "" && client.AuthMethod == "" {
			return errors.New("registered login client secret is missing its auth method")
		}
		if err := validateRegistrationConsistency(
			&Registration{
				ClientID:                client.ID,
				ClientSecret:            client.Secret,
				TokenEndpointAuthMethod: client.AuthMethod,
			},
		); err != nil {
			return err
		}
	default:
		return fmt.Errorf("login record has unknown client source %q", client.Source)
	}
	return nil
}

func LoginEligible(login *Login, sc config.ServerConfig) (bool, string) {
	if sc.UnsetEnv != nil {
		return false, sc.UnsetEnv.Error()
	}
	if err := ValidateLogin(login); err != nil {
		return false, err.Error()
	}
	resource, err := CanonicalResourceURI(sc.URL)
	if err != nil || resource != login.Resource {
		return false, "the server URL changed"
	}
	configuredID := ""
	configuredTokenURL := ""
	if sc.Auth != nil {
		configuredID = sc.Auth.ClientID
		configuredTokenURL = sc.Auth.TokenURL
	}
	if configuredID != "" {
		if login.Client.Source != ClientConfigured || login.Client.ID != configuredID {
			return false, "the configured OAuth client changed"
		}
	} else if login.Client.Source == ClientConfigured {
		return false, "the configured OAuth client was removed"
	}
	if configuredTokenURL != "" && configuredTokenURL != login.TokenURL {
		return false, "the OAuth token endpoint changed"
	}
	return true, ""
}

func LoadLogin(configDir, serverName string) (*Login, error) {
	if !config.ValidServerName.MatchString(serverName) {
		return nil, fmt.Errorf("invalid server name: %q", serverName)
	}
	data, err := os.ReadFile(tokenPath(configDir, serverName))
	if err != nil {
		return nil, err
	}
	var login Login
	if err := json.Unmarshal(data, &login); err != nil {
		return nil, fmt.Errorf("decode login record: %w", err)
	}
	if err := ValidateLogin(&login); err != nil {
		return nil, err
	}
	return &login, nil
}

func saveLogin(configDir, serverName string, login *Login) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}
	if err := ValidateLogin(login); err != nil {
		return err
	}
	path := tokenPath(configDir, serverName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(login, "", "  ")
	if err != nil {
		return err
	}
	return fileio.ReplaceFile(path, data, fileio.ReplaceOptions{Perm: 0o600})
}

func IsNotFound(err error) bool { return errors.Is(err, fs.ErrNotExist) }

func ReadLoginState(configDir, serverName string, sc config.ServerConfig) (LoginState, error) {
	login, err := LoadLogin(configDir, serverName)
	snapshot := EvaluateLogin(login, err, sc)
	if snapshot.State == Unreadable || snapshot.State == ForOtherSettings {
		return snapshot.State, errors.New(snapshot.Reason)
	}
	return snapshot.State, nil
}

func ReadLoginSnapshot(configDir, serverName string, sc config.ServerConfig) LoginSnapshot {
	login, err := LoadLogin(configDir, serverName)
	return EvaluateLogin(login, err, sc)
}

func EvaluateLogin(login *Login, loadErr error, sc config.ServerConfig) LoginSnapshot {
	return EvaluateLoginAt(LoginEvaluation{Login: login, LoadErr: loadErr, Server: sc, Now: clock.System().Now()})
}

func EvaluateLoginAt(p LoginEvaluation) LoginSnapshot {
	if IsNotFound(p.LoadErr) {
		return LoginSnapshot{State: Missing, Reason: "login is missing"}
	}
	if p.LoadErr != nil {
		return LoginSnapshot{Login: p.Login, State: Unreadable, Reason: p.LoadErr.Error()}
	}
	if eligible, reason := LoginEligible(p.Login, p.Server); !eligible {
		return LoginSnapshot{Login: p.Login, State: ForOtherSettings, Reason: reason}
	}
	if tokenValidAt(p.Login.Token, p.Now) {
		return LoginSnapshot{Login: p.Login, State: Valid}
	}
	if p.Login.Token.RefreshToken != "" {
		return LoginSnapshot{Login: p.Login, State: Refreshable}
	}
	return LoginSnapshot{Login: p.Login, State: Expired, Reason: "login is expired and has no refresh token"}
}

func tokenValidAt(token *oauth2.Token, now time.Time) bool {
	return token != nil && token.AccessToken != "" &&
		(token.Expiry.IsZero() || !now.Add(10*time.Second).After(token.Expiry))
}

func DeleteCredentials(configDir, serverName string) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}
	var errs []error
	for _, path := range CredentialPaths(configDir, serverName) {
		if err := os.Remove(path); err != nil && !IsNotFound(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func CredentialPaths(configDir, serverName string) []string {
	return []string{tokenPath(configDir, serverName), registrationPath(configDir, serverName)}
}

func tokenPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".token.json")
}

func (l *Login) RefreshConfig(configured *config.AuthConfig) *config.AuthConfig {
	ac := configured.Clone()
	ac.ClientID = l.Client.ID
	ac.ClientSecret = l.Client.Secret
	ac.TokenEndpointAuthMethod = l.Client.AuthMethod
	if l.Client.Source == ClientConfigured {
		ac.ClientSecret = configured.ClientSecret
		ac.TokenEndpointAuthMethod = configured.TokenEndpointAuthMethod
	}
	ac.TokenURL = l.TokenURL
	ac.ResourceURL = l.Resource
	return ac
}
