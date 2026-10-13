package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/fileio"
)

// TokenSaveWait outlasts the longest a refresh holds the lock (the provider bounds one at 10s), so a
// finished browser login waits out a refresh in progress instead of being dropped.
const TokenSaveWait = 30 * time.Second

// TokenLock serializes reading, refreshing and writing a server's token across mini processes: a
// rotating refresh token used twice gets the whole login revoked.
type TokenLock struct {
	ConfigDir  string
	ServerName string
	// Clock paces polling for the lock; deadlines come from the caller's context.
	Clock clock.Clock
}

// TokenFile is a server's token file while its TokenLock is held.
type TokenFile struct {
	configDir  string
	serverName string
}

func (f TokenFile) Load() (*oauth2.Token, error) { return Load(f.configDir, f.serverName) }

func (f TokenFile) Save(t *oauth2.Token) error { return save(f.configDir, f.serverName, t) }

// Do runs fn holding the lock, waiting for another holder until ctx ends. If the lock can't be taken
// at all, fn runs unlocked: a network mount without file locks keeps working, unprotected.
func (l TokenLock) Do(ctx context.Context, fn func(TokenFile) error) error {
	if !config.ValidServerName.MatchString(l.ServerName) {
		return fmt.Errorf("invalid server name: %q", l.ServerName)
	}
	file := TokenFile{configDir: l.ConfigDir, serverName: l.ServerName}
	unlock, err := l.acquire(ctx)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return fmt.Errorf("wait for another mini process to finish with %s's token: %w", l.ServerName, err)
	}
	if err != nil {
		slog.Warn("oauth token lock unavailable; continuing without it", "server", l.ServerName, "err", err)
		return fn(file)
	}
	defer unlock()
	return fn(file)
}

// Save writes t under the lock, waiting up to TokenSaveWait for a refresh in progress.
func (l TokenLock) Save(ctx context.Context, t *oauth2.Token) error {
	ctx, cancel := context.WithTimeout(ctx, TokenSaveWait)
	defer cancel()
	return l.Do(ctx, func(f TokenFile) error { return f.Save(t) })
}

func (l TokenLock) acquire(ctx context.Context) (unlock func(), err error) {
	path := tokenLockPath(l.ConfigDir, l.ServerName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return fileio.LockFile(ctx, l.Clock, path)
}

// The lock file is never deleted: removing a file someone holds splits the lock in two.
func tokenLockPath(configDir, serverName string) string {
	return filepath.Join(configDir, "internal", serverName+".token.lock")
}
