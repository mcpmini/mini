package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/fileio"
)

var ErrConfigKeptChanging = fileio.ErrKeptChanging

// EditFile applies edit with a backup; unchanged files return an empty backup path.
func EditFile(path string, edit func([]byte) ([]byte, error), now time.Time) (string, error) {
	var backup string
	_, err := fileio.EditFile(fileio.EditParams{
		Path: path,
		Edit: edit,
		BeforeReplace: func(target string, original []byte) (func(), error) {
			written, err := writeBackup(target, original, now)
			if err != nil {
				return nil, fmt.Errorf("back up %s: %w", target, err)
			}
			backup = written
			return func() {
				os.Remove(written) //nolint:errcheck // the file is untouched, so a leftover backup is only clutter
			}, nil
		},
	})
	if err != nil {
		return "", err
	}
	return backup, nil
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
