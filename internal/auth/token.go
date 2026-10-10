package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/fileio"
)

// Tokens are stored as 0600 plaintext JSON. Same pattern used by:
//   - kubectl:   https://github.com/kubernetes/client-go/blob/47b97e8b/tools/clientcmd/loader.go#L465
//   - AWS CLI:   https://github.com/aws/aws-cli/blob/2baa4c8d/awscli/customizations/configure/writer.py#L65
//   - gh CLI:    https://github.com/cli/go-gh/blob/55692c6b/pkg/config/config.go#L165
//   - Heroku:    https://github.com/heroku/heroku-cli-command/blob/08b784b7/src/login.ts#L329
//
// To upgrade: swap Load/Save to use zalando/go-keyring (OS keychain) with file fallback for CI.
func Load(configDir, serverName string) (*oauth2.Token, error) {
	if !config.ValidServerName.MatchString(serverName) {
		return nil, fmt.Errorf("invalid server name: %q", serverName)
	}
	data, err := os.ReadFile(tokenPath(configDir, serverName))
	if err != nil {
		return nil, err
	}
	var t oauth2.Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func Save(configDir, serverName string, t *oauth2.Token) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}
	path := tokenPath(configDir, serverName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return fileio.ReplaceFile(path, data, fileio.ReplaceOptions{Perm: 0o600})
}

func IsNotFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

type TokenState int

const (
	TokenMissing TokenState = iota + 1
	TokenUnreadable
	// TokenExpired means expired with no refresh token.
	TokenExpired
	// TokenRefreshable means expired, but the runtime refreshes it silently.
	TokenRefreshable
	TokenValid
)

func (s TokenState) NeedsLogin() bool {
	return s != TokenValid && s != TokenRefreshable
}

// ReadTokenState reads the stored token only; it never contacts the server.
// err is non-nil only with TokenUnreadable and says why.
func ReadTokenState(configDir, serverName string) (TokenState, error) {
	token, err := Load(configDir, serverName)
	if IsNotFound(err) {
		return TokenMissing, nil
	}
	if err != nil {
		return TokenUnreadable, err
	}
	if token.Valid() {
		return TokenValid, nil
	}
	if token.RefreshToken != "" {
		return TokenRefreshable, nil
	}
	return TokenExpired, nil
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

// CredentialPaths are the files holding a server's login: its token and its registered client.
func CredentialPaths(configDir, serverName string) []string {
	return []string{tokenPath(configDir, serverName), registrationPath(configDir, serverName)}
}

func tokenPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".token.json")
}
