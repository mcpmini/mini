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
	dir := filepath.Join(configDir, "internal")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tokenPath(configDir, serverName), data, 0600)
}

func IsNotFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

func tokenPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".token.json")
}
