package agents

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxEditAttempts = 3

var ErrConfigKeptChanging = errors.New("the file kept changing while mini edited it")

var errChangedDuringEdit = errors.New("changed during edit")

// EditFile replaces an agent's config with edit's result, keeping a backup of exactly the bytes
// edit saw. Agents rewrite their configs while running, so an attempt that finds the file changed
// before its rename is discarded and retried. It returns the backup's path, or "" when the edit
// changed nothing and the file was left alone.
func EditFile(path string, edit func([]byte) ([]byte, error), now time.Time) (string, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	for range maxEditAttempts {
		backup, err := editOnce(target, edit, now)
		if !errors.Is(err, errChangedDuringEdit) {
			return backup, err
		}
	}
	return "", fmt.Errorf("%s: %w", path, ErrConfigKeptChanging)
}

func editOnce(path string, edit func([]byte) ([]byte, error), now time.Time) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	edited, err := edit(original)
	if err != nil || bytes.Equal(edited, original) {
		return "", err
	}
	backup, err := writeBackup(path, original, now)
	if err != nil {
		return "", fmt.Errorf("back up %s: %w", path, err)
	}
	if err := replaceIfUnchanged(path, original, edited, info.Mode().Perm()); err != nil {
		os.Remove(backup) //nolint:errcheck // the file is untouched, so a leftover backup is only clutter
		return "", err
	}
	return backup, nil
}

func replaceIfUnchanged(path string, original, edited []byte, mode os.FileMode) error {
	tmp, err := writeTemp(path, edited, mode)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err == nil && !bytes.Equal(current, original) {
		err = errChangedDuringEdit
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp) //nolint:errcheck // the edit already failed; a leftover temp file is only clutter
	}
	return err
}

func writeTemp(path string, data []byte, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".mini-*")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(data)
	err = errors.Join(err, tmp.Chmod(mode), tmp.Close())
	if err != nil {
		os.Remove(tmp.Name()) //nolint:errcheck // the write already failed
		return "", err
	}
	return tmp.Name(), nil
}

// Backups hold the agent's tokens, so they are created 0600 and never overwrite an earlier one.
func writeBackup(path string, data []byte, now time.Time) (string, error) {
	backup := backupPath(path, "")
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		backup = backupPath(path, now.Format("20060102T150405"))
		f, err = os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err = errors.Join(err, f.Close()); err != nil {
		os.Remove(backup) //nolint:errcheck // a partial backup must not be mistaken for a full one
		return "", err
	}
	return backup, nil
}

// backupPath keeps the extension last so editors still recognize the format:
// config.toml → config.minibackup.toml, .claude.json → .claude.minibackup.json.
func backupPath(path, stamp string) string {
	dir, name := filepath.Split(path)
	ext := filepath.Ext(name)
	if ext == name {
		ext = ""
	}
	parts := []string{strings.TrimSuffix(name, ext), "minibackup"}
	if stamp != "" {
		parts = append(parts, stamp)
	}
	return filepath.Join(dir, strings.Join(parts, ".")+ext)
}
