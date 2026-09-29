package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

const projectionPollInterval = 5 * time.Second

// StartProjectionReload applies projection YAML edits to a live server without
// restart. Stops when ctx is canceled.
func (s *Server) StartProjectionReload(ctx context.Context) {
	go s.runProjectionReload(ctx, nil)
}

type reloadState struct {
	fingerprint map[string]string
	servers     map[string]config.ServerConfig
}

func (s *Server) runProjectionReload(ctx context.Context, afterCheck func()) {
	fingerprint, _ := s.fingerprintOrWarn()
	state := reloadState{fingerprint: fingerprint, servers: s.loadReconcileBaseline()}
	ticker := s.clock.NewTicker(projectionPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.Chan():
			state = s.reloadIfProjectionFilesChanged(ctx, state)
			if afterCheck != nil {
				afterCheck()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) reloadIfProjectionFilesChanged(ctx context.Context, state reloadState) reloadState {
	current, ok := s.fingerprintOrWarn()
	if !ok {
		return state
	}
	changed := changedPaths(state.fingerprint, current)
	if len(changed) == 0 {
		return state
	}
	plan, servers := s.planFromDisk(state.servers)
	s.removeReconciled(plan.remove)
	_, fresh := s.applyReload()
	s.connectReconciled(ctx, plan.connect)
	if len(fresh) > 0 {
		s.logger.Info("projections reloaded", "files", changed)
	}
	return reloadState{fingerprint: current, servers: servers}
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
