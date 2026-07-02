package response

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/randutil"
)

func (s *Store) WriteRaw(raw []byte) (string, error) {
	path, err := s.writeBytes(prettyJSON(raw))
	if err != nil {
		return "", err
	}
	name := filepath.Base(path)
	return strings.TrimSuffix(name, filepath.Ext(name)), nil
}

func (s *Store) writeBytes(b []byte) (string, error) {
	base := s.newFileBase()
	path, err := s.createUniqueFile(base, b)
	if err != nil {
		return "", err
	}
	s.recordWrite(path, int64(len(b)))
	return path, nil
}

func (s *Store) recordWrite(path string, size int64) {
	s.mu.Lock()
	s.files = append(s.files, storedFile{path: path, size: size, expires: s.clock.Now().Add(s.ttl)})
	s.usedBytes += size
	toRemove := s.evictOvershoot()
	s.mu.Unlock()
	s.restoreRemoveFailed(toRemove)
}

func prettyJSON(b []byte) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return b
	}
	return buf.Bytes()
}

func (s *Store) newFileBase() string {
	return fmt.Sprintf("%d", s.clock.Now().UnixMilli())
}

func (s *Store) createUniqueFile(base string, b []byte) (string, error) {
	const maxAttempts = 5
	for i := range maxAttempts {
		name := base
		if i > 0 {
			name = base + "_" + randutil.HexString(2)
		}
		path := filepath.Join(s.dir, name+".json")
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
	// O_EXCL makes create atomic at the OS level — no TOCTOU race between
	// checking existence and opening; exactly one caller wins per path.
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
