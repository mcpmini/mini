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
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return replaceTokenFile(path, data)
}

func replaceTokenFile(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err == nil {
		return nil
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func IsNotFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

type AuthorizationNeed struct {
	Needed bool
	Note   string
}

func NeedsAuthorization(configDir string, sc config.ServerConfig) AuthorizationNeed {
	if sc.Auth == nil || sc.Auth.Type != config.AuthTypeOAuth2 {
		return AuthorizationNeed{}
	}
	token, err := Load(configDir, sc.Name)
	if IsNotFound(err) {
		return AuthorizationNeed{Needed: true}
	}
	if err != nil {
		return AuthorizationNeed{Needed: true, Note: fmt.Sprintf("token unreadable: %v", err)}
	}
	// An expired token with a refresh token is not flagged; the HTTP transport handles silent refresh.
	if token.Valid() || token.RefreshToken != "" {
		return AuthorizationNeed{}
	}
	return AuthorizationNeed{Needed: true}
}

func tokenPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".token.json")
}
