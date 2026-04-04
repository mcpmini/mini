package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func runCleanup(configDir string) {
	cfg, _, err := config.Load(configDir)
	if err != nil {
		fatalf("load config: %v", err)
	}
	dir, ttl := resolveResponseDir(cfg, configDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no response directory found")
			return
		}
		fatalf("read %s: %v", dir, err)
	}
	removed, freed := cleanExpiredFiles(dir, entries, time.Now().Add(-ttl))
	if removed == 0 {
		fmt.Println("nothing to clean up")
	} else {
		fmt.Printf("removed %d file pair(s), freed %.1f MB\n", removed, float64(freed)/1e6)
	}
}

func resolveResponseDir(cfg *config.Config, configDir string) (string, time.Duration) {
	dir := cfg.ResponseDir
	if dir == "" {
		dir = filepath.Join(configDir, "responses")
	}
	ttl, err := time.ParseDuration(cfg.ResponseTTL)
	if err != nil {
		ttl = 7 * 24 * time.Hour
	}
	return dir, ttl
}

func cleanExpiredFiles(dir string, entries []os.DirEntry, cutoff time.Time) (removed int, freed int64) {
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".raw.json") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		rawPath := strings.TrimSuffix(path, ".json") + ".raw.json"
		freed += info.Size()
		os.Remove(path)
		if ri, err := os.Stat(rawPath); err == nil {
			freed += ri.Size()
			os.Remove(rawPath)
		}
		removed++
	}
	return removed, freed
}
