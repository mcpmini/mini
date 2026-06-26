package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestInterpolateServerConfig(t *testing.T) {
	t.Run("header substituted", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("GITHUB_TOKEN", "mytoken123")
		writeFile(t, filepath.Join(dir, "servers", "gh.yaml"), `
name: gh
transport: http
url: https://api.github.com
headers:
  Authorization: "Bearer ${GITHUB_TOKEN}"
`)
		sc := mustLoadOneServer(t, dir)
		if sc.Headers["Authorization"] != "Bearer mytoken123" {
			t.Errorf("expected header substituted, got %q", sc.Headers["Authorization"])
		}
	})

	t.Run("missing var errors", func(t *testing.T) {
		dir := t.TempDir()
		os.Unsetenv("MISSING_VAR_XYZ")
		writeFile(t, filepath.Join(dir, "servers", "gh.yaml"), `
name: gh
transport: http
url: https://api.github.com/${MISSING_VAR_XYZ}
`)
		_, _, err := config.Load(dir)
		if err == nil {
			t.Fatal("expected error for missing env var")
		}
		if !strings.Contains(err.Error(), "MISSING_VAR_XYZ") {
			t.Errorf("expected error to mention MISSING_VAR_XYZ, got: %v", err)
		}
	})

	t.Run("multiple vars substituted", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("SRV_URL", "https://example.com")
		t.Setenv("SRV_TOKEN", "tok456")
		writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `
name: svc
transport: http
url: ${SRV_URL}
headers:
  Authorization: "Bearer ${SRV_TOKEN}"
`)
		sc := mustLoadOneServer(t, dir)
		if sc.URL != "https://example.com" {
			t.Errorf("expected URL substituted, got %q", sc.URL)
		}
		if sc.Headers["Authorization"] != "Bearer tok456" {
			t.Errorf("expected header substituted, got %q", sc.Headers["Authorization"])
		}
	})

	t.Run("no vars unchanged", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "servers", "plain.yaml"), `
name: plain
command: my-mcp
`)
		sc := mustLoadOneServer(t, dir)
		if sc.Command != "my-mcp" {
			t.Errorf("expected command unchanged, got %q", sc.Command)
		}
	})

	t.Run("duplicate missing var deduplicated", func(t *testing.T) {
		dir := t.TempDir()
		os.Unsetenv("MISSING_XYZ")
		writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `
name: svc
transport: http
url: https://${MISSING_XYZ}/api
headers:
  Authorization: "Bearer ${MISSING_XYZ}"
`)
		_, _, err := config.Load(dir)
		if err == nil {
			t.Fatal("expected error for missing env var")
		}
		count := strings.Count(err.Error(), "MISSING_XYZ")
		if count != 1 {
			t.Errorf("expected MISSING_XYZ to appear once in error, got %d times: %v", count, err)
		}
	})
}

func TestInterpolateMainConfig(t *testing.T) {
	t.Run("response_dir substituted", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("HOME", "/testhome")
		writeFile(t, filepath.Join(dir, "config.yaml"), `response_dir: ${HOME}/mydir`)
		cfg, _ := mustLoadConfig(t, dir)
		if cfg.ResponseDir != "/testhome/mydir" {
			t.Errorf("expected response_dir substituted, got %q", cfg.ResponseDir)
		}
	})

	t.Run("missing var errors", func(t *testing.T) {
		dir := t.TempDir()
		os.Unsetenv("MISSING_MAIN_VAR")
		writeFile(t, filepath.Join(dir, "config.yaml"), `log_level: ${MISSING_MAIN_VAR}`)
		_, _, err := config.Load(dir)
		if err == nil {
			t.Fatal("expected error for missing env var")
		}
		if !strings.Contains(err.Error(), "MISSING_MAIN_VAR") {
			t.Errorf("expected error to mention MISSING_MAIN_VAR, got: %v", err)
		}
	})
}

func TestInterpolateActionConfig(t *testing.T) {
	t.Run("default_args substituted", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("MY_TOKEN", "secretval")
		writeFile(t, filepath.Join(dir, "internal", "actions", "myaction.yaml"), `
name: myaction
server: gh
tool: list_issues
default_args:
  token: ${MY_TOKEN}
`)
		ac := mustLoadOneAction(t, dir)
		if ac.DefaultArgs["token"] != "secretval" {
			t.Errorf("expected token substituted, got %v", ac.DefaultArgs["token"])
		}
	})
}

func TestProjectionNotInterpolated(t *testing.T) {
	dir := t.TempDir()
	os.Unsetenv("UNSET_PROJ_VAR_XXXX")
	writeFile(t, filepath.Join(dir, "servers", "svc.yaml"), `name: svc
command: my-mcp`)
	writeFile(t, filepath.Join(dir, "servers", "svc.proj.yaml"), `
list_issues:
  include_only: [number, title]
  format: "${UNSET_PROJ_VAR_XXXX}"
`)
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections["list_issues"]
	if proj == nil {
		t.Fatal("expected list_issues projection to be loaded")
		return
	}
	if proj.Format != "${UNSET_PROJ_VAR_XXXX}" {
		t.Errorf("expected projection format to be literal, got %q", proj.Format)
	}
}
