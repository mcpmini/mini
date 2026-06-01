package response

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WriteRaw writes already-marshalled JSON bytes to disk as pretty-printed JSON.
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

// WritePair writes a slim .json file and a full .raw.json file atomically.
// The slim file's _meta.raw is set to the raw path before marshaling.
// Returns the slim path (what agents should read).
func (s *Store) WritePair(slimData map[string]any, rawJSON []byte) (string, error) {
	rawJSON = prettyJSON(rawJSON)
	slimJSON, err := json.MarshalIndent(slimData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal slim: %w", err)
	}
	s.evictIfNeeded(int64(len(slimJSON) + len(rawJSON)))
	return s.openPairWithSlim(newTimestampBase(), slimData, slimJSON, rawJSON)
}

func (s *Store) openPairWithSlim(base string, slimData map[string]any, slimJSON, rawJSON []byte) (string, error) {
	const maxAttempts = 200
	for i := range maxAttempts {
		path, done, err := s.tryWritePair(base, i, slimData, slimJSON, rawJSON)
		if done {
			return path, err
		}
	}
	return "", fmt.Errorf("write pair: name collision for %s", base)
}

func (s *Store) tryWritePair(base string, attempt int, slimData map[string]any, slimJSON, rawJSON []byte) (string, bool, error) {
	slimPath, rawPath := pairPaths(s.dir, base, attempt)
	finalJSON, err := injectRawPath(slimData, slimJSON, rawPath)
	if err != nil {
		return "", true, err
	}
	size, err := s.writePairFiles(slimPath, rawPath, finalJSON, rawJSON)
	if retryPairWrite(err) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	s.recordPair(slimPath, rawPath, size)
	return slimPath, true, nil
}

func pairPaths(dir, base string, attempt int) (string, string) {
	b := uniqueBase(base, attempt)
	return filepath.Join(dir, b+".json"), filepath.Join(dir, b+".raw.json")
}

func retryPairWrite(err error) bool {
	return os.IsExist(err)
}

func (s *Store) recordPair(slimPath, rawPath string, size int64) {
	s.mu.Lock()
	s.files = append(s.files, storedFile{path: slimPath, rawPath: rawPath, size: size, expires: time.Now().Add(s.ttl)})
	s.usedBytes += size
	toRemove := s.evictOvershoot()
	s.mu.Unlock()
	removeFiles(toRemove)
}

func injectRawPath(slimData map[string]any, slimJSON []byte, rawPath string) ([]byte, error) {
	meta, ok := slimData["_meta"].(map[string]any)
	if !ok {
		return slimJSON, nil
	}
	// Copy both maps before mutating to avoid side-effecting the caller's data.
	metaCopy := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		metaCopy[k] = v
	}
	metaCopy["raw"] = rawPath
	slimCopy := make(map[string]any, len(slimData))
	for k, v := range slimData {
		slimCopy[k] = v
	}
	slimCopy["_meta"] = metaCopy
	b, err := json.MarshalIndent(slimCopy, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal slim: %w", err)
	}
	return b, nil
}

func (s *Store) writePairFiles(slimPath, rawPath string, slimJSON, rawJSON []byte) (int64, error) {
	if err := writeSlimFile(slimPath, slimJSON); err != nil {
		return 0, err
	}
	if err := writeRawFile(rawPath, rawJSON, slimPath); err != nil {
		return 0, err
	}
	return int64(len(slimJSON) + len(rawJSON)), nil
}

func writeSlimFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close() //nolint:errcheck
		_ = os.Remove(path)
		return fmt.Errorf("write slim: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close slim: %w", err)
	}
	return nil
}

func writeRawFile(rawPath string, rawJSON []byte, slimPath string) error {
	if err := os.WriteFile(rawPath, rawJSON, 0600); err != nil {
		os.Remove(slimPath)
		os.Remove(rawPath)
		return fmt.Errorf("write raw: %w", err)
	}
	return nil
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
