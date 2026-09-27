//go:build test

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func writeProjLoadFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjections(t *testing.T) {
	t.Run("happy path: servers/ + proj.yaml overlay + inline", func(t *testing.T) {
		dir := t.TempDir()
		// toolA inline, toolB only in proj.yaml — overlay must merge both tools
		writeProjLoadFile(t, dir, "servers/a.yaml", "name: a\ncommand: echo\nprojections:\n  toolA:\n    include_only: [x]\n")
		writeProjLoadFile(t, dir, "servers/a.proj.yaml", "toolB:\n  include_only: [y]\n")
		writeProjLoadFile(t, dir, "config.yaml", "servers:\n- name: b\n  command: echo\n  projections:\n    toolB:\n      include_only: [z]\n")

		load := config.LoadProjections(dir)

		if len(load.Skipped) != 0 {
			t.Fatalf("expected no skipped servers, got %v", load.Skipped)
		}
		if load.Projections["a"]["toolA"] == nil {
			t.Errorf("toolA (inline) missing after proj.yaml overlay: %v", load.Projections["a"])
		}
		if load.Projections["a"]["toolB"] == nil {
			t.Errorf("toolB (proj.yaml) missing after overlay: %v", load.Projections["a"])
		}
		bp := load.Projections["b"]["toolB"]
		if bp == nil || len(bp.IncludeOnly) != 1 {
			t.Errorf("inline projection not loaded for b: %+v", bp)
		}
	})

	t.Run("servers/ file beats inline entry with same name", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/svc.yaml", "name: svc\ncommand: echo\nprojections:\n  t:\n    include_only: [file]\n")
		writeProjLoadFile(t, dir, "config.yaml", "servers:\n- name: svc\n  command: echo\n  projections:\n    t:\n      include_only: [inline]\n")

		load := config.LoadProjections(dir)

		p := load.Projections["svc"]["t"]
		if p == nil || len(p.IncludeOnly) != 1 || p.IncludeOnly[0] != "file" {
			t.Errorf("servers/ should win over inline, got %+v", p)
		}
	})

	t.Run("orphan proj.yaml is ignored", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/orphan.proj.yaml", "tool:\n  include_only: [x]\n")

		load := config.LoadProjections(dir)

		if _, ok := load.Projections["orphan"]; ok {
			t.Error("orphan .proj.yaml should be ignored")
		}
		if len(load.Skipped) != 0 {
			t.Errorf("orphan .proj.yaml should not cause a skip, got %v", load.Skipped)
		}
	})

	t.Run("undefined ${VAR} in server file still loads projections", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/svc.yaml",
			"name: svc\nurl: https://api.example.com\nheaders:\n  Authorization: Bearer ${UNDEFINED_TOKEN_XYZ}\nprojections:\n  t:\n    include_only: [a]\n")

		load := config.LoadProjections(dir)

		if len(load.Skipped) != 0 {
			t.Errorf("undefined env var should not block projection load, skipped: %v", load.Skipped)
		}
		if load.Projections["svc"]["t"] == nil {
			t.Error("projection not loaded despite undefined env var in server file")
		}
	})

	t.Run("malformed servers/b.yaml: b skipped, a loaded", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/a.yaml", "name: a\ncommand: echo\nprojections:\n  t:\n    include_only: [ok]\n")
		writeProjLoadFile(t, dir, "servers/b.yaml", "bad: [yaml\n")

		load := config.LoadProjections(dir)

		if _, ok := load.Skipped["b"]; !ok {
			t.Error("malformed b.yaml should be skipped")
		}
		if load.Projections["a"]["t"] == nil {
			t.Error("a should still load when b fails")
		}
		if load.KeepsPrevious("b") != true {
			t.Error("KeepsPrevious(b) should be true")
		}
		if load.KeepsPrevious("a") != false {
			t.Error("KeepsPrevious(a) should be false")
		}
	})

	t.Run("malformed b.proj.yaml: b skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/b.yaml", "name: b\ncommand: echo\nprojections:\n  t:\n    include_only: [inline]\n")
		writeProjLoadFile(t, dir, "servers/b.proj.yaml", "bad: [yaml\n")

		load := config.LoadProjections(dir)

		if _, ok := load.Skipped["b"]; !ok {
			t.Error("malformed b.proj.yaml should cause b to be skipped")
		}
		if load.KeepsPrevious("b") != true {
			t.Error("KeepsPrevious(b) should be true after proj.yaml failure")
		}
		if _, ok := load.Projections["b"]; ok {
			t.Error("a skipped server must not get partial (inline-only) projections")
		}
	})

	t.Run("invalid format in projection: skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/svc.yaml", "name: svc\ncommand: echo\nprojections:\n  t:\n    format: bad-format\n")

		load := config.LoadProjections(dir)

		if _, ok := load.Skipped["svc"]; !ok {
			t.Error("invalid format should cause svc to be skipped")
		}
		if load.KeepsPrevious("svc") != true {
			t.Error("KeepsPrevious(svc) should be true after format failure")
		}
	})

	t.Run("malformed config.yaml: KeepsPrevious true for inline-only, false for file servers", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/file-svc.yaml", "name: file-svc\ncommand: echo\n")
		writeProjLoadFile(t, dir, "config.yaml", "bad: [yaml\n")

		load := config.LoadProjections(dir)

		if load.KeepsPrevious("inline-only") != true {
			t.Error("KeepsPrevious(inline-only) should be true when config.yaml fails")
		}
		if load.KeepsPrevious("file-svc") != false {
			t.Error("KeepsPrevious(file-svc) should be false: it came from servers/ file, not config.yaml")
		}
	})
}
