package response

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) loadExisting() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		s.loadEntry(e, now)
	}
}

func (s *Store) loadEntry(e os.DirEntry, now time.Time) {
	name := e.Name()
	if e.IsDir() || filepath.Ext(name) != ".json" || strings.Contains(name, ".raw.") {
		return
	}
	created, ok := parseTimestamp(name)
	if !ok {
		return
	}
	s.storeEntryIfFresh(e, name, created, now)
}

func (s *Store) storeEntryIfFresh(e os.DirEntry, name string, created, now time.Time) {
	path := filepath.Join(s.dir, name)
	rawPath := strings.TrimSuffix(path, ".json") + ".raw.json"
	expires := created.Add(s.ttl)
	if expires.Before(now) {
		warnRemoveErr(os.Remove(path))
		warnRemoveErr(os.Remove(rawPath))
		return
	}
	s.appendEntryInfo(e, path, rawPath, expires)
}

func (s *Store) appendEntryInfo(e os.DirEntry, path, rawPath string, expires time.Time) {
	info, err := e.Info()
	if err != nil {
		return
	}
	actualRaw, size := rawInfo(rawPath, info.Size())
	s.files = append(s.files, storedFile{path: path, rawPath: actualRaw, size: size, expires: expires})
	s.usedBytes += size
}

func rawInfo(rawPath string, slimSize int64) (string, int64) {
	rs := fileSize(rawPath)
	if rs == 0 {
		return "", slimSize
	}
	return rawPath, slimSize + rs
}

func fileSize(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

// parseTimestamp extracts the creation time from a timestamp-named file.
// Expects names like "20260313142530123.json" (17 chars base + .json).
// The leading 14 chars are YYYYMMDDHHMMSS; next 3 are milliseconds.
func parseTimestamp(name string) (time.Time, bool) {
	base := name[:len(name)-len(filepath.Ext(name))]
	if len(base) < 17 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(tsLayout, base[:14], time.UTC)
	return t, err == nil
}
