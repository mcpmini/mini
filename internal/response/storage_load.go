package response

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Store) loadExisting() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	now := s.clk.Now()
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
	expires := created.Add(s.ttl)
	if expires.Before(now) {
		warnRemoveErr(os.Remove(path))
		return
	}
	info, err := e.Info()
	if err != nil {
		return
	}
	s.files = append(s.files, storedFile{path: path, size: info.Size(), expires: expires})
	s.usedBytes += info.Size()
}

func parseTimestamp(name string) (time.Time, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	part := base
	if idx := strings.IndexByte(base, '_'); idx > 0 {
		part = base[:idx]
	}
	n, err := strconv.ParseInt(part, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(n), true
}
