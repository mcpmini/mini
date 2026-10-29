package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/fileio"
)

var challengeMu sync.Mutex

type Challenge struct {
	ServerURL       string `json:"server_url"`
	WWWAuthenticate string `json:"www_authenticate"`
	StatusCode      int    `json:"status_code"`
}

func saveChallenge(dir, name string, challenge Challenge) error {
	challengeMu.Lock()
	defer challengeMu.Unlock()
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name: %q", name)
	}
	if !challenge.valid() {
		return errors.New("invalid authorization challenge")
	}
	if err := os.MkdirAll(filepath.Dir(challengePath(dir, name)), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(challenge)
	if err != nil {
		return err
	}
	return fileio.ReplaceFile(challengePath(dir, name), data, fileio.ReplaceOptions{Perm: 0o600})
}

func loadChallenge(dir, name, serverURL string) (*Challenge, error) {
	if !config.ValidServerName.MatchString(name) {
		return nil, fmt.Errorf("invalid server name: %q", name)
	}
	data, err := os.ReadFile(challengePath(dir, name))
	if err != nil {
		return nil, err
	}
	var challenge Challenge
	if err := json.Unmarshal(data, &challenge); err != nil || !challenge.valid() {
		return nil, errors.New("invalid authorization challenge")
	}
	if challenge.ServerURL != serverURL {
		return nil, os.ErrNotExist
	}
	return &challenge, nil
}

func (c Challenge) valid() bool {
	return c.ServerURL != "" && ValidAuthorizationChallenge(c.StatusCode, c.WWWAuthenticate)
}

func ValidAuthorizationChallenge(status int, header string) bool {
	return (status == http.StatusUnauthorized || status == http.StatusForbidden) &&
		bearerChallenge(header) && (status != http.StatusForbidden ||
		strings.EqualFold(parseWWWAuthParam(header, "error"), "insufficient_scope"))
}

func bearerChallenge(header string) bool {
	return bearerStart(header) >= 0
}

func challengePath(dir, name string) string {
	return filepath.Join(dir, "internal", name+".challenge.json")
}

func RecordAuthorizationChallenge(dir string, sc config.ServerConfig, challenge Challenge) {
	if sc.Auth == nil || sc.Auth.Type != config.AuthTypeOAuth2 || sc.Auth.Token != "" ||
		!ValidAuthorizationChallenge(challenge.StatusCode, challenge.WWWAuthenticate) {
		return
	}
	challenge.ServerURL = sc.URL
	if err := saveChallenge(dir, sc.Name, challenge); err != nil {
		slog.Warn("could not persist authorization challenge", "server", sc.Name, "error", err)
	}
}

func clearConsumedChallenge(dir, name string, consumed *Challenge) {
	if consumed == nil {
		return
	}
	challengeMu.Lock()
	defer challengeMu.Unlock()
	current, err := loadChallenge(dir, name, consumed.ServerURL)
	if err != nil || *current != *consumed {
		return
	}
	if err := os.Remove(challengePath(dir, name)); err != nil && !IsNotFound(err) {
		slog.Warn("could not remove completed authorization challenge", "server", name, "error", err)
	}
}
