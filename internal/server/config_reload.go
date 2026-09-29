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

const configPollInterval = 5 * time.Second

// ConfigBaseline is the config on disk that a running server was started from.
type ConfigBaseline struct {
	fingerprint map[string]string
	servers     map[string]config.ServerConfig
}

// CaptureConfigBaseline must run before the startup config.Load: an edit made after
// it then reaches the server as a change instead of being absorbed into the baseline.
func CaptureConfigBaseline(configDir string) ConfigBaseline {
	fingerprint, _ := fingerprintConfigSources(configDir)
	_, servers := planReconcile(nil, config.LoadServerSet(configDir))
	return ConfigBaseline{fingerprint: fingerprint, servers: servers}
}

// StartConfigReload applies server and projection edits on disk to a live server
// without restart. Stops when ctx is canceled.
func (s *Server) StartConfigReload(ctx context.Context, baseline ConfigBaseline) {
	go s.runConfigReload(ctx, baseline, nil)
}

func (s *Server) runConfigReload(ctx context.Context, state ConfigBaseline, afterCheck func()) {
	ticker := s.clock.NewTicker(configPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.Chan():
			state = s.reloadIfConfigChanged(ctx, state)
			if afterCheck != nil {
				afterCheck()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) reloadIfConfigChanged(ctx context.Context, state ConfigBaseline) ConfigBaseline {
	current, ok := s.fingerprintOrWarn()
	if !ok {
		return state
	}
	changed := changedPaths(state.fingerprint, current)
	if len(changed) == 0 {
		return state
	}
	plan, servers := s.planFromDisk(state.servers)
	s.detachReconciled(plan)
	_, fresh := s.applyReload()
	s.connectReconciled(ctx, append(plan.replace, plan.connect...))
	if len(fresh) > 0 {
		s.logger.Info("projections reloaded", "files", changed)
	}
	return ConfigBaseline{fingerprint: current, servers: servers}
}

func (s *Server) fingerprintOrWarn() (map[string]string, bool) {
	fp, err := fingerprintConfigSources(s.configDir)
	if err != nil {
		s.logger.Warn("config reload: fingerprint config sources", "err", err)
		return nil, false
	}
	return fp, true
}

func fingerprintConfigSources(configDir string) (map[string]string, error) {
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
