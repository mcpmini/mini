package initcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/fileio"
)

// stage is a copy of mini's config that a run writes to, so neither mini nor its daemon sees the
// run's servers or logins until commit.
type stage struct {
	configDir string
	dir       string
	copied map[string][]byte
}

// Probes and logins read mini's settings and the existing servers' credentials, so those are copied too.
var stagedFiles = []string{
	"config.yaml",
	"servers/*.yaml",
	"internal/*.token.json",
	"internal/*.dcr.json",
	"internal/*.meta.json",
}

var (
	errChangedOutsideInit = errors.New("changed outside init while it ran; kept that version")
	errKeptNewer          = errors.New("kept the newer copy in the config dir")
)

func newStage(configDir string) (*stage, error) {
	// Outside the config dir, so a run that quits leaves it as it found it, even one that didn't exist.
	dir, err := os.MkdirTemp("", "mini-init-")
	if err != nil {
		return nil, fmt.Errorf("create init stage: %w", err)
	}
	s := &stage{configDir: configDir, dir: dir, copied: map[string][]byte{}}
	if err := errors.Join(s.makeDirs(), s.copyIn()); err != nil {
		return nil, errors.Join(err, s.discard())
	}
	return s, nil
}

func (s *stage) makeDirs() error {
	for _, sub := range []string{"servers", "internal"} {
		if err := os.Mkdir(filepath.Join(s.dir, sub), 0o700); err != nil {
			return fmt.Errorf("create init stage: %w", err)
		}
	}
	return nil
}

func (s *stage) copyIn() error {
	paths, err := stagedPaths(s.configDir)
	if err != nil {
		return err
	}
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(s.configDir, rel))
		if err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
		if err := writeStaged(filepath.Join(s.dir, rel), data); err != nil {
			return err
		}
		s.copied[rel] = data
	}
	return nil
}

// commit writes each file the run changed into the config dir. A file changed there since the copy
// keeps its version: the run's change would overwrite what someone else just did.
func (s *stage) commit() []ServerError {
	paths, err := stagedPaths(s.dir)
	if err != nil {
		return []ServerError{{Name: "init", Err: err}}
	}
	var failed []ServerError
	// Sorted, internal/ goes before servers/: a daemon that sees a new server finds its login already there.
	for _, rel := range union(paths, slices.Collect(maps.Keys(s.copied))) {
		if err := s.commitFile(rel); err != nil && !errors.Is(err, errKeptNewer) {
			failed = append(failed, ServerError{Name: serverOf(rel), Err: err})
		}
	}
	return failed
}

func (s *stage) commitFile(rel string) error {
	staged, inStage, err := readIfExists(filepath.Join(s.dir, rel))
	if err != nil {
		return err
	}
	copied, wasCopied := s.copied[rel]
	if inStage == wasCopied && bytes.Equal(staged, copied) {
		return nil
	}
	path := filepath.Join(s.configDir, rel)
	unchanged := func() error { return s.unchangedSinceCopy(rel) }
	if !inStage {
		if err := unchanged(); err != nil {
			return err
		}
		return removeIfExists(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return fileio.ReplaceFile(path, staged, fileio.ReplaceOptions{Perm: 0o600, BeforeRename: unchanged})
}

func (s *stage) unchangedSinceCopy(rel string) error {
	current, exists, err := readIfExists(filepath.Join(s.configDir, rel))
	if err != nil {
		return err
	}
	copied, wasCopied := s.copied[rel]
	if exists != wasCopied || !bytes.Equal(current, copied) {
		// Only a server file's change is worth reporting; a token the daemon refreshed is just newer.
		if strings.HasPrefix(rel, "servers/") {
			return errChangedOutsideInit
		}
		return errKeptNewer
	}
	return nil
}

func (s *stage) discard() error {
	return os.RemoveAll(s.dir)
}

func stagedPaths(root string) ([]string, error) {
	var paths []string
	for _, pattern := range stagedFiles {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			rel, err := filepath.Rel(root, match)
			if err != nil {
				return nil, err
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
	}
	return paths, nil
}

func writeStaged(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("stage %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("stage %s: %w", path, err)
	}
	return nil
}

func readIfExists(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func union(a, b []string) []string {
	all := slices.Concat(a, b)
	slices.Sort(all)
	return slices.Compact(all)
}

func serverOf(rel string) string {
	name, _, _ := strings.Cut(filepath.Base(rel), ".")
	return name
}
