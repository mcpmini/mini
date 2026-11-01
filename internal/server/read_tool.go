package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/jq"
)

func (s *Server) handleRead(ctx context.Context, raw json.RawMessage) (any, error) {
	file, filter, err := parseReadArgs(raw)
	if err != nil {
		return nil, err
	}
	file = s.resolveReadPath(file)
	if validationErr := s.validateStorePath(file); validationErr != nil {
		return nil, validationErr
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w: response file not found or unreadable", errInvalidParams)
	}
	if filter == "" {
		return string(b), nil
	}
	out, err := jq.Eval(ctx, b, filter)
	if err != nil {
		return nil, fmt.Errorf("%w: read filter: %w", errInvalidParams, err)
	}
	return out, nil
}

func (s *Server) resolveReadPath(file string) string {
	if filepath.Ext(file) == "" {
		file = file + ".json"
	}
	return filepath.Join(s.store.Dir(), file)
}

func parseReadArgs(raw json.RawMessage) (file, filter string, err error) {
	var p struct {
		File   string `json:"file"`
		Filter string `json:"filter"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", fmt.Errorf("%w: read: %w", errInvalidParams, err)
	}
	if p.File == "" {
		return "", "", fmt.Errorf("%w: read: file is required", errInvalidParams)
	}
	return p.File, p.Filter, nil
}

func (s *Server) validateStorePath(path string) error {
	return validateStorePathWith(validateStorePathParams{
		StoreDir: s.store.Dir(),
		Path:     path,
		Resolve:  resolveSymlinks,
	})
}

type validateStorePathParams struct {
	StoreDir string
	Path     string
	Resolve  func(string) (string, error)
}

func validateStorePathWith(p validateStorePathParams) error {
	// EvalSymlinks on both sides prevents a symlink inside the store from escaping
	// confinement; on macOS, TempDir itself is a symlink (/var/... → /private/var/...).
	storeDir, err := p.Resolve(p.StoreDir)
	if err != nil {
		return fmt.Errorf("%w: read: resolve response directory: %w", errInvalidParams, err)
	}
	abs, err := p.Resolve(p.Path)
	if err != nil {
		return fmt.Errorf("%w: read: resolve response path: %w", errInvalidParams, err)
	}
	if !strings.HasPrefix(abs, storeDir+string(filepath.Separator)) {
		return fmt.Errorf("%w: read: path must be within mini response directory", errInvalidParams)
	}
	return nil
}

func resolveSymlinks(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return filepath.Abs(path)
}
