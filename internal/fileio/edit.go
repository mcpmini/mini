package fileio

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const editAttempts = 3

var ErrKeptChanging = errors.New("the file kept changing while mini edited it")

var errChangedDuringEdit = errors.New("changed during edit")

type EditParams struct {
	Path string
	// Edit gets a copy of the file's bytes. Returning them unchanged writes nothing; returning nil is an error.
	Edit func([]byte) ([]byte, error)
	// BeforeReplace runs once Edit changed the bytes, just before the file is replaced; the undo it
	// returns runs if the replace then fails.
	BeforeReplace func(target string, original []byte) (undo func(), err error)
}

// EditFile edits the file Path resolves to and keeps its mode. When the file or its symlink changes
// between the read and the replace, the edit runs again on the new bytes, so no one's write is lost.
func EditFile(p EditParams) (changed bool, err error) {
	for range editAttempts {
		changed, err := editOnce(p)
		if !errors.Is(err, errChangedDuringEdit) {
			return changed, err
		}
	}
	return false, fmt.Errorf("%s: %w", p.Path, ErrKeptChanging)
}

type editSource struct {
	path, target string
	original     []byte
	mode         os.FileMode
}

func editOnce(p EditParams) (bool, error) {
	source, err := readEditSource(p.Path)
	if err != nil {
		return false, err
	}
	edited, err := p.Edit(bytes.Clone(source.original))
	if err == nil && edited == nil {
		err = errors.New("edit returned nil data")
	}
	if err != nil || bytes.Equal(edited, source.original) {
		return false, err
	}
	undo, err := beforeReplace(p, source)
	if err != nil {
		return false, err
	}
	if err := ReplaceFile(source.target, edited, ReplaceOptions{Perm: source.mode, BeforeRename: source.checkUnchanged}); err != nil {
		undo()
		return false, err
	}
	return true, nil
}

func beforeReplace(p EditParams, source editSource) (func(), error) {
	if p.BeforeReplace == nil {
		return func() {}, nil
	}
	undo, err := p.BeforeReplace(source.target, source.original)
	if undo == nil {
		undo = func() {}
	}
	return undo, err
}

func readEditSource(path string) (editSource, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return editSource{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return editSource{}, err
	}
	original, err := os.ReadFile(target)
	if err != nil {
		return editSource{}, err
	}
	return editSource{path: path, target: target, original: original, mode: info.Mode().Perm()}, nil
}

func (s editSource) checkUnchanged() error {
	target, err := filepath.EvalSymlinks(s.path)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if target != s.target || !bytes.Equal(current, s.original) {
		return errChangedDuringEdit
	}
	return nil
}
