package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/jqfilter"
)

func (s *Server) handleRead(ctx context.Context, raw json.RawMessage) (any, error) {
	path, filter, err := parseReadArgs(raw)
	if err != nil {
		return nil, err
	}
	if err := s.validateStorePath(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if filter == "" {
		return string(b), nil
	}
	return applyReadFilter(ctx, b, filter)
}

func applyReadFilter(ctx context.Context, b []byte, filter string) (any, error) {
	var data any
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, fmt.Errorf("%w: read: filter requires valid JSON content: %w", errInvalidParams, err)
	}
	out, err := jqfilter.Run(ctx, data, filter)
	if err != nil {
		return nil, fmt.Errorf("%w: read: %w", errInvalidParams, err)
	}
	return out, nil
}

func parseReadArgs(raw json.RawMessage) (path, filter string, err error) {
	var p struct {
		Path   string `json:"path"`
		Filter string `json:"filter"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", fmt.Errorf("%w: read: %w", errInvalidParams, err)
	}
	if p.Path == "" {
		return "", "", fmt.Errorf("%w: read: path is required", errInvalidParams)
	}
	return p.Path, p.Filter, nil
}

func (s *Server) validateStorePath(path string) error {
	// EvalSymlinks resolves symlinks on both sides so a symlink inside the store
	// dir pointing outside it cannot escape the confinement. On macOS, TempDir
	// returns /var/... which is itself a symlink to /private/var/..., so both
	// sides must be resolved for the prefix check to work correctly.
	storeDir := resolveSymlinks(s.store.Dir())
	abs := resolveSymlinks(path)
	if !strings.HasPrefix(abs, storeDir+string(filepath.Separator)) {
		return fmt.Errorf("%w: read: path must be within mini response directory", errInvalidParams)
	}
	return nil
}

// resolveSymlinks resolves symlinks, falling back to filepath.Abs if the path
// does not exist yet (file written but not yet visible, or non-existent path).
func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	abs, _ := filepath.Abs(path)
	return abs
}
