//go:build test

package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestNewCallStoreUsesConfiguredDirectory(t *testing.T) {
	root := t.TempDir()
	configuredDir := filepath.Join(root, "responses")
	store, err := newCallStore(callStoreParams{
		Config:    &config.Config{ResponseDir: configuredDir},
		ConfigDir: root,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:     clock.NewFake(),
	})
	if err != nil {
		t.Fatalf("newCallStore: %v", err)
	}
	defer store.Close()
	if store.Dir() != configuredDir {
		t.Fatalf("store dir = %q, want %q", store.Dir(), configuredDir)
	}
	assertPrivateCallStoreDir(t, configuredDir)
}

func TestNewCallStoreFallsBackToTempDirectory(t *testing.T) {
	root := t.TempDir()
	tempDir := t.TempDir()
	configuredFile := filepath.Join(root, "responses")
	testutil.WriteFile(t, configuredFile, "")
	t.Setenv("TMPDIR", tempDir)
	fallbackDir := filepath.Join(tempDir, "mini-responses")
	store, err := newCallStore(callStoreParams{
		Config:    &config.Config{ResponseDir: filepath.Join(configuredFile, "child")},
		ConfigDir: root,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:     clock.NewFake(),
	})
	if err != nil {
		t.Fatalf("newCallStore: %v", err)
	}
	defer store.Close()
	if store.Dir() != fallbackDir {
		t.Fatalf("store dir = %q, want fallback %q", store.Dir(), fallbackDir)
	}
	assertPrivateCallStoreDir(t, fallbackDir)
}

func TestNewCallStoreReportsFallbackFailure(t *testing.T) {
	root := t.TempDir()
	tempDir := t.TempDir()
	configuredFile := filepath.Join(root, "responses")
	testutil.WriteFile(t, configuredFile, "")
	t.Setenv("TMPDIR", tempDir)
	testutil.WriteFile(t, filepath.Join(tempDir, "mini-responses"), "")
	store, err := newCallStore(callStoreParams{
		Config:    &config.Config{ResponseDir: filepath.Join(configuredFile, "child")},
		ConfigDir: root,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:     clock.NewFake(),
	})
	if store != nil {
		store.Close()
		t.Fatal("newCallStore returned a store after both directories failed")
	}
	if err == nil {
		t.Fatal("newCallStore returned nil error after both directories failed")
	}
}

func assertPrivateCallStoreDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store directory: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("store directory mode = %04o, want 0700", got)
	}
}
