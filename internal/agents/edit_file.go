package agents

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/fileio"
)

const maxEditAttempts = 3

var ErrConfigKeptChanging = errors.New("the file kept changing while mini edited it")

var errChangedDuringEdit = errors.New("changed during edit")

// EditFile applies edit with a backup; unchanged files return an empty backup path.
func EditFile(path string, edit func([]byte) ([]byte, error), now time.Time) (string, error) {
	for range maxEditAttempts {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
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
	edited, err := editConfigBytes(original, edit)
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

func editConfigBytes(original []byte, edit func([]byte) ([]byte, error)) ([]byte, error) {
	edited, err := edit(bytes.Clone(original))
	if err == nil && edited == nil {
		return nil, errors.New("config edit returned nil data")
	}
	return edited, err
}

func replaceIfUnchanged(path string, original, edited []byte, mode os.FileMode) error {
	return fileio.ReplaceFile(path, edited, fileio.ReplaceOptions{
		Perm: mode,
		BeforeRename: func() error {
			current, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(current, original) {
				return errChangedDuringEdit
			}
			return nil
		},
	})
}

func writeBackup(path string, data []byte, now time.Time) (string, error) {
	backup := backupPath(path, "")
	err := fileio.CreateFile(backup, data, 0o600)
	if errors.Is(err, os.ErrExist) {
		backup = backupPath(path, now.Format("20060102T150405"))
		err = fileio.CreateFile(backup, data, 0o600)
	}
	if err != nil {
		return "", err
	}
	return backup, nil
}

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
