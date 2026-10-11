package initcmd

import (
	"cmp"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/fileio"
)

// stage holds the servers a run adds, and their logins, so neither mini nor its daemon sees them
// until commit. Servers mini already has stay where they are: a token refreshed while probing one
// must reach mini even when the run quits.
type stage struct {
	configDir string
	dir       string
	created   bool
}

var errAddedOutsideInit = errors.New("added to mini outside init while it ran; kept that version")

func newStage(configDir string) *stage {
	// Beside mini's own tokens, so a stage a crash leaves behind is no more exposed than they are.
	return &stage{configDir: configDir, dir: filepath.Join(configDir, "internal", "init-stage-"+rand.Text())}
}

// create waits for the first save, so a run that quits before it leaves the config dir as it found it.
func (s *stage) create() error {
	if s.created {
		return nil
	}
	sweepStaleStages(s.configDir)
	for _, sub := range []string{"servers", "internal"} {
		if err := os.MkdirAll(filepath.Join(s.dir, sub), 0o700); err != nil {
			return fmt.Errorf("create init stage: %w", err)
		}
	}
	// Probes and logins in the stage read mini's settings.
	settings, exists, err := readIfExists(filepath.Join(s.configDir, "config.yaml"))
	if err == nil && exists {
		err = writeFile(filepath.Join(s.dir, "config.yaml"), settings)
	}
	s.created = err == nil
	return err
}

// A run killed before it could discard its stage leaves it behind, logins included; no run lasts a day.
const staleStageAge = 24 * time.Hour

func sweepStaleStages(configDir string) {
	pattern := filepath.Join(configDir, "internal", "init-stage-*")
	stages, _ := filepath.Glob(pattern) //nolint:errcheck // Glob fails only on a bad pattern, and this one is fixed
	for _, dir := range stages {
		info, err := os.Stat(dir)
		if err != nil || clock.System().Since(info.ModTime()) < staleStageAge {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("init: remove %s: %v", dir, err)
		}
	}
}

// commit moves each named server into mini, its login and state first, so a daemon that sees the
// server finds its login already there.
func (s *stage) commit(names []string) (committed []string, failed []ServerError) {
	for _, name := range names {
		if err := s.commitServer(name); err != nil {
			failed = append(failed, ServerError{Name: name, Err: err})
			continue
		}
		committed = append(committed, name)
	}
	return committed, failed
}

func (s *stage) commitServer(name string) error {
	// Checked before the login is written over: on a case-insensitive disk Linear's files are linear's.
	if taken, err := serverNameTaken(s.configDir, name); err != nil || taken {
		return cmp.Or(err, errAddedOutsideInit)
	}
	staged, mini := serverState(s.dir, name), serverState(s.configDir, name)
	for i := range staged {
		if err := replaceOrRemove(mini[i], staged[i]); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(config.ServerPath(s.dir, name))
	if err != nil {
		return err
	}
	path := config.ServerPath(s.configDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Created, not replaced: a server added since the check above is someone else's.
	return fileio.CreateFile(path, data, 0o600)
}

func serverNameTaken(configDir, name string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(configDir, "servers"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool {
		return strings.EqualFold(e.Name(), name+".yaml")
	}), nil
}

func serverState(configDir, name string) []string {
	return append(auth.CredentialPaths(configDir, name), config.ServerMetaPath(configDir, name))
}

// A file the stage lacks is removed too: mini may still hold a token from an earlier server of the same name.
func replaceOrRemove(path, from string) error {
	data, staged, err := readIfExists(from)
	switch {
	case err != nil:
		return err
	case !staged:
		return removeIfExists(path)
	}
	return writeFile(path, data)
}

func (s *stage) discard() error {
	return os.RemoveAll(s.dir)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return fileio.ReplaceFile(path, data, fileio.ReplaceOptions{Perm: 0o600})
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
