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

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/fileio"
	"github.com/mcpmini/mini/internal/ops"
)

// stage holds the servers a run adds, and their logins, so neither mini nor its daemon sees them
// until commit. Servers mini already has stay where they are: a token refreshed while probing one
// must reach mini even when the run quits.
type stage struct {
	configDir string
	dir       string
	created   bool
	// madeDirs are the config dir and internal/ when create made them, deepest first.
	madeDirs []string
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
	s.noteMissingDirs()
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

// A quit then leaves a config dir that didn't exist as missing as it found it.
func (s *stage) noteMissingDirs() {
	for _, dir := range []string{filepath.Join(s.configDir, "internal"), s.configDir} {
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			s.madeDirs = append(s.madeDirs, dir)
		}
	}
}

// touch keeps a stage in use from looking stale to another run's sweep.
func (s *stage) touch() {
	now := clock.System().Now()
	if err := os.Chtimes(s.dir, now, now); err != nil {
		log.Printf("init: touch %s: %v", s.dir, err)
	}
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
func (s *stage) commit(names []string) commitResult {
	var result commitResult
	for _, name := range names {
		if err := s.commitServer(name); err != nil {
			result.failed = append(result.failed, ServerError{Name: name, Err: err})
			continue
		}
		result.committed = append(result.committed, name)
	}
	return result
}

type commitResult struct {
	committed []string
	failed    []ServerError
}

func (s *stage) commitServer(name string) error {
	// Checked before the login is written over: on a case-insensitive disk Linear's files are linear's.
	if taken, err := serverNameTaken(s.configDir, name); err != nil || taken {
		return cmp.Or(err, errAddedOutsideInit)
	}
	sc, err := config.ReadUnexpandedServer(s.dir, name)
	if err != nil {
		return err
	}
	// Mini may still hold a login from an earlier server of the same name.
	if err := ops.ForgetServerState(s.configDir, name); err != nil {
		return err
	}
	if err := s.copyState(name); err != nil {
		return err
	}
	// Created, not replaced: a server added in the moment since the name check is someone else's. Its
	// login may already be overwritten; holding a lock across mini's commands would be the only cure.
	_, err = config.CreateServerFile(s.configDir, sc)
	if errors.Is(err, fs.ErrExist) {
		return errAddedOutsideInit
	}
	return err
}

func (s *stage) copyState(name string) error {
	staged, mini := ops.ServerStatePaths(s.dir, name), ops.ServerStatePaths(s.configDir, name)
	for i := range staged {
		data, exists, err := readIfExists(staged[i])
		if err == nil && exists {
			err = writeFile(mini[i], data)
		}
		if err != nil {
			return err
		}
	}
	return nil
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

func (s *stage) discard() error {
	if err := os.RemoveAll(s.dir); err != nil {
		return err
	}
	for _, dir := range s.madeDirs {
		os.Remove(dir) //nolint:errcheck // fails only when something else now lives there, which then stays
	}
	return nil
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
