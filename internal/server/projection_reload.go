package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const projectionPollInterval = 5 * time.Second

// StartProjectionReload applies projection YAML edits to a live server without
// restart. Stops when ctx is canceled.
func (s *Server) StartProjectionReload(ctx context.Context) {
	go s.runProjectionReload(ctx, nil)
}

func (s *Server) runProjectionReload(ctx context.Context, afterCheck func()) {
	ticker := s.clock.NewTicker(projectionPollInterval)
	defer ticker.Stop()
	last, _ := s.fingerprintOrWarn()
	for {
		select {
		case <-ticker.Chan():
			last = s.reloadIfProjectionFilesChanged(last)
			if afterCheck != nil {
				afterCheck()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) reloadIfProjectionFilesChanged(last map[string]string) map[string]string {
	current, ok := s.fingerprintOrWarn()
	if !ok {
		return last
	}
	changed := changedPaths(last, current)
	if len(changed) == 0 {
		return last
	}
	if _, err := s.reloadProjections(); err != nil {
		s.logger.Warn("projection reload failed, keeping previous projections", "err", err)
	} else {
		s.logger.Info("projections reloaded", "files", changed)
	}
	return current
}

func (s *Server) fingerprintOrWarn() (map[string]string, bool) {
	fp, err := fingerprintProjectionSources(s.configDir)
	if err != nil {
		s.logger.Warn("projection reload: fingerprint projection sources", "err", err)
		return nil, false
	}
	return fp, true
}

func fingerprintProjectionSources(configDir string) (map[string]string, error) {
	paths, err := filepath.Glob(filepath.Join(configDir, "servers", "*.yaml"))
	if err != nil {
		return nil, err
	}
	fp := make(map[string]string, len(paths)+1)
	for _, p := range paths {
		if err := addFileHashIfPresent(fp, p); err != nil {
			return nil, err
		}
	}
	if err := addFileHashIfPresent(fp, filepath.Join(configDir, "config.yaml")); err != nil {
		return nil, err
	}
	return fp, nil
}

func addFileHash(fp map[string]string, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	fp[path] = hex.EncodeToString(sum[:])
	return nil
}

func addFileHashIfPresent(fp map[string]string, path string) error {
	err := addFileHash(fp, path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func changedPaths(prev, curr map[string]string) []string {
	var changed []string
	for p, h := range curr {
		if prev[p] != h {
			changed = append(changed, p)
		}
	}
	for p := range prev {
		if _, ok := curr[p]; !ok {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return changed
}
