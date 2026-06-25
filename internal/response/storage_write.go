package response

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WriteRaw pretty-prints before writing — "raw" refers to the unprocessed upstream bytes, not the on-disk format.
func (s *Store) WriteRaw(raw []byte) (string, error) {
	return s.writeBytes(prettyJSON(raw))
}

func (s *Store) writeBytes(b []byte) (string, error) {
	s.evictIfNeeded(int64(len(b)))

	base := newTimestampBase()
	path, err := s.openUnique(base, b)
	if err != nil {
		return "", err
	}
	s.recordWrite(path, int64(len(b)))
	return path, nil
}

func (s *Store) recordWrite(path string, size int64) {
	s.mu.Lock()
	s.files = append(s.files, storedFile{path: path, size: size, expires: time.Now().Add(s.ttl)})
	s.usedBytes += size
	toRemove := s.evictOvershoot()
	s.mu.Unlock()
	removeFiles(toRemove)
}

func prettyJSON(b []byte) []byte {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return b
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return b
	}
	return pretty
}

func newTimestampBase() string {
	now := time.Now().UTC()
	return fmt.Sprintf("%s%03d", now.Format(tsLayout), now.Nanosecond()/1_000_000)
}

// openUnique writes b to base+".json". On collision it retries with a numeric
// suffix (_0001, _0002, …) until it finds a free slot or exceeds maxAttempts.
// O_EXCL guarantees atomicity: concurrent callers each advance through a
// different suffix, so up to maxAttempts goroutines can collide on the same
// millisecond without error.
func (s *Store) openUnique(base string, b []byte) (string, error) {
	const maxAttempts = 200
	for i := range maxAttempts {
		path := filepath.Join(s.dir, uniqueBase(base, i)+".json")
		if err := writeExclusive(path, b); os.IsExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("write response file: name collision for %s", base)
}

func writeExclusive(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close() //nolint:errcheck
		os.Remove(path)
		return fmt.Errorf("write response file: %w", err)
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(path)
		return fmt.Errorf("write response file: %w", cerr)
	}
	return nil
}
