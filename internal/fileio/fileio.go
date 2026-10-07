package fileio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ReplaceOptions controls the replacement file's mode and final check.
type ReplaceOptions struct {
	Perm os.FileMode
	// BeforeRename runs the final caller check and may abort replacement.
	BeforeRename func() error
}

type file interface {
	io.WriteCloser
	Name() string
	Chmod(os.FileMode) error
}

type replaceParams struct {
	path       string
	data       []byte
	opts       ReplaceOptions
	createTemp func(string, string) (file, error)
}

// ReplaceFile replaces path with data. An error from BeforeRename is returned unchanged.
func ReplaceFile(path string, data []byte, opts ReplaceOptions) error {
	return replaceFile(replaceParams{
		path: path, data: data, opts: opts,
		createTemp: func(dir, pattern string) (file, error) {
			return os.CreateTemp(dir, pattern)
		},
	})
}

func replaceFile(p replaceParams) (err error) {
	tmp, err := p.createTemp(filepath.Dir(p.path), "."+filepath.Base(p.path)+".mini-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			//nolint:errcheck // temp cleanup cannot replace the staged write or replacement error.
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = stage(tmp, p.data, p.opts.Perm); err != nil {
		return err
	}
	if p.opts.BeforeRename != nil {
		if err = p.opts.BeforeRename(); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), p.path)
}

func stage(f file, data []byte, perm os.FileMode) error {
	writeErr := write(f, data)
	chmodErr := f.Chmod(perm)
	closeErr := f.Close()
	return errors.Join(writeErr, chmodErr, closeErr)
}

type createParams struct {
	path string
	data []byte
	perm os.FileMode
	open func(string, int, os.FileMode) (file, error)
}

// CreateFile creates path only when it does not exist and reports cleanup errors after failures.
func CreateFile(path string, data []byte, perm os.FileMode) error {
	return createFile(createParams{
		path: path, data: data, perm: perm,
		open: func(path string, flags int, perm os.FileMode) (file, error) {
			return os.OpenFile(path, flags, perm)
		},
	})
}

func createFile(p createParams) error {
	f, err := p.open(p.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, p.perm)
	if err != nil {
		return err
	}
	writeErr := write(f, p.data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return errors.Join(err, os.Remove(p.path))
	}
	return nil
}

func write(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
