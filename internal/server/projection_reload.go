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

// StartProjectionReload polls the config dir so projection YAML edits apply
// to a running server without a restart. The poller stops when ctx is canceled.
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
		// Advancing to the current fingerprint even on failure means a bad file
		// warns once, not every tick; the next edit is still detected by hash.
		s.logger.Warn("projection reload failed, keeping previous projections", "err", err)
	} else {
		s.logger.Info("projections reloaded", "files", changed)
	}
	return current
}

func (s *Server) fingerprintOrWarn() (map[string]string, bool) {
	fp, err := fingerprintServerFiles(s.configDir)
	if err != nil {
		s.logger.Warn("projection reload: fingerprint server files", "err", err)
		return nil, false
	}
	return fp, true
}

// fingerprintServerFiles hashes every servers/*.yaml — which also matches
// *.proj.yaml — covering both inline `projections:` blocks and projection files.
func fingerprintServerFiles(configDir string) (map[string]string, error) {
	paths, err := filepath.Glob(filepath.Join(configDir, "servers", "*.yaml"))
	if err != nil {
		return nil, err
	}
	fp := make(map[string]string, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		fp[p] = hex.EncodeToString(sum[:])
	}
	return fp, nil
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
