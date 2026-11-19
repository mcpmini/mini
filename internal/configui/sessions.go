package configui

import (
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/randutil"
)

const (
	entryTTL        = time.Minute
	idleTimeout     = time.Hour
	sessionCap      = 8 * time.Hour
	janitorInterval = time.Minute
)

type entry struct {
	servers []string
	expires time.Time
}

type session struct {
	servers    []string
	created    time.Time
	lastActive time.Time
}

func (s *session) expiresAt() time.Time {
	idle, capped := s.lastActive.Add(idleTimeout), s.created.Add(sessionCap)
	if idle.Before(capped) {
		return idle
	}
	return capped
}

func (s *session) allows(server string) bool { return slices.Contains(s.servers, server) }

func newSecret() string { return base64.RawURLEncoding.EncodeToString(randutil.Bytes(32)) }

// secretKey indexes the maps by a hash of the secret, so a map lookup's timing reveals nothing about it.
func secretKey(secret string) [sha256.Size]byte { return sha256.Sum256([]byte(secret)) }
