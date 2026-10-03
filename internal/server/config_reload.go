package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

const configPollInterval = 5 * time.Second

// StartConfigReload applies server and projection edits on disk to a live
// server without restart. Stops when ctx is canceled.
func (s *Server) StartConfigReload(ctx context.Context) {
	go s.runConfigReload(ctx, nil)
}

func (s *Server) runConfigReload(ctx context.Context, afterCheck func()) {
	if afterCheck == nil {
		afterCheck = func() {}
	}
	last, _ := s.fingerprintOrWarn()
	// Startup read the config before this poller existed; an edit made in
	// between is only caught by applying the config once now.
	s.applyConfig()
	ticker := s.clock.NewTicker(configPollInterval)
	defer ticker.Stop()
	afterCheck()
	for {
		select {
		case <-ticker.Chan():
			last = s.reloadIfConfigChanged(last)
			afterCheck()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) reloadIfConfigChanged(last map[string]string) map[string]string {
	current, ok := s.fingerprintOrWarn()
	if !ok {
		return last
	}
	changed := changedPaths(last, current)
	if len(changed) == 0 {
		return last
	}
	if fresh := s.applyConfig(); len(fresh) > 0 {
		s.logger.Info("projections reloaded", "files", changed)
	}
	return current
}

func (s *Server) applyConfig() map[string]int {
	s.removeServersGoneFromConfig()
	_, fresh, err := s.applyReload()
	if err != nil {
		s.logger.Warn("config reload: keeping the current config", "err", err)
	}
	return fresh
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
	paths, err := config.ServerDirFiles(configDir)
	if err != nil {
		return nil, err
	}
	fp := make(map[string]string, len(paths))
	for _, p := range paths {
		if h, ok := fileFingerprint(p); ok {
			fp[p] = h
		}
	}
	return fp, nil
}

const unreadableFingerprint = "unreadable"

func fileFingerprint(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		// Not a scan failure: the loader reports it and holds back only this file's server.
		return unreadableFingerprint, true
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
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
