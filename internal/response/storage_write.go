package response

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"
)

// WriteRaw pretty-prints before writing — "raw" refers to the unprocessed upstream bytes, not the on-disk format.
func (s *Store) WriteRaw(raw []byte) (string, error) {
	return s.writeBytes(prettyJSON(raw))
}

func (s *Store) writeBytes(b []byte) (string, error) {
	base := newFileBase()
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

func newFileBase() string {
	now := time.Now()
	var r [4]byte
	rand.Read(r[:]) //nolint:errcheck
	h := fnv.New32a()
	fmt.Fprintf(h, "%d", now.UnixNano())
	h.Write(r[:])
	return fmt.Sprintf("%d_%08x", now.Unix(), h.Sum32())
}

// openUnique writes b to base+".json", retrying up to 3 times on collision.
// O_EXCL guarantees atomicity; collisions should not occur in practice with random filenames.
func (s *Store) openUnique(base string, b []byte) (string, error) {
	const maxAttempts = 3
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
