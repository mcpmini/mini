//go:build test

package config_test

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

	t.Run("broken orphan proj.yaml is ignored: no Skipped, no SourceError", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/orphan.proj.yaml", "bad: [yaml\n")

		load := config.LoadProjections(dir)

		if len(load.Skipped) != 0 {
			t.Errorf("broken orphan .proj.yaml should not cause a skip, got %v", load.Skipped)
		}
		if len(load.SourceErrors) != 0 {
			t.Errorf("broken orphan .proj.yaml should not cause a source error, got %v", load.SourceErrors)
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

	t.Run("lenient interpolation substitutes defined vars", func(t *testing.T) {
		t.Run("defined var in name", func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PROJ_DEFINED_NAME_XYZ", "myserver")
			writeProjLoadFile(t, dir, "servers/svc.yaml", "name: ${PROJ_DEFINED_NAME_XYZ}\ncommand: echo\n")

			load := config.LoadProjections(dir)

			if len(load.SourceErrors) != 0 {
				t.Errorf("expected no errors, got %v", load.SourceErrors)
			}
			if load.KeepsPrevious("myserver") {
				t.Error("KeepsPrevious(myserver) should be false: defined var was substituted and server loaded")
			}
		})
		t.Run("defined var in inline projection field", func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PROJ_DEFINED_FIELD_XYZ", "myfield")
			writeProjLoadFile(t, dir, "servers/svc.yaml",
				"name: svc\ncommand: echo\nprojections:\n  getData:\n    include_only: [${PROJ_DEFINED_FIELD_XYZ}]\n")

			load := config.LoadProjections(dir)

			p := load.Projections["svc"]["getData"]
			if p == nil || len(p.IncludeOnly) == 0 || p.IncludeOnly[0] != "myfield" {
				t.Errorf("expected projection with substituted field value, got %+v", p)
			}
		})
	})

	t.Run("flow-sequence ${VAR} loads correctly with and without var set", func(t *testing.T) {
		cases := []struct {
			name   string
			setVar bool
		}{
			{"var-unset", false},
			{"var-set", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				writeProjLoadFile(t, dir, "servers/svc.yaml",
					"name: svc\ncommand: echo\nargs: [--token, ${PROJ_TEST_TOK_XYZ}]\nprojections:\n  t:\n    include_only: [a]\n")
				if tc.setVar {
					t.Setenv("PROJ_TEST_TOK_XYZ", "testtoken")
				}
				load := config.LoadProjections(dir)
				if len(load.Skipped) != 0 || len(load.SourceErrors) != 0 {
					t.Errorf("expected no errors, skipped=%v sourceErrors=%v", load.Skipped, load.SourceErrors)
				}
				if load.Projections["svc"]["t"] == nil {
					t.Error("projection not loaded for flow-sequence server file")
				}
			})
		}
	})

	t.Run("file stem differs from name: bad format → Skipped by real name", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/github-server.yaml",
			"name: github\ncommand: echo\nprojections:\n  t:\n    format: bad-format\n")

		load := config.LoadProjections(dir)

		if _, ok := load.Skipped["github"]; !ok {
			t.Errorf("Skipped should use real name 'github', not file stem 'github-server'; Skipped=%v", load.Skipped)
		}
		if _, ok := load.Projections["github"]; ok {
			t.Errorf("github should be absent from Projections after format error")
		}
	})

	t.Run("broken servers/github.yaml: inline twin not treated as fresh", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/github.yaml", "bad: [yaml\n")
		writeProjLoadFile(t, dir, "config.yaml", "servers:\n- name: github\n  command: echo\n")

		load := config.LoadProjections(dir)

		if len(load.SourceErrors) == 0 {
			t.Fatal("expected source error for broken servers/github.yaml")
		}
		if !load.KeepsPrevious("github") {
			t.Error("KeepsPrevious(github) should be true: inline twin must not count as fresh when servers/ file failed")
		}
		if _, ok := load.Projections["github"]; ok {
			t.Error("github should be absent from Projections (inline twin has no projections)")
		}
	})

	t.Run("duplicate names across two servers/ files: first wins", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/a-svc.yaml", "name: svc\ncommand: echo\nprojections:\n  t:\n    include_only: [first]\n")
		writeProjLoadFile(t, dir, "servers/b-svc.yaml", "name: svc\ncommand: echo\nprojections:\n  t:\n    include_only: [second]\n")

		load := config.LoadProjections(dir)
		_, servers, err := config.Load(dir)
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}

		var loadProj map[string]*config.ProjectionConfig
		for _, s := range servers {
			if s.Name == "svc" {
				loadProj = s.Projections
				break
			}
		}
		if loadProj == nil {
			t.Fatal("config.Load: svc not found")
		}
		if len(load.Projections["svc"]) == 0 {
			t.Fatal("LoadProjections: svc not found")
		}
		got := load.Projections["svc"]["t"]
		want := loadProj["t"]
		if got == nil || want == nil || !reflect.DeepEqual(got.IncludeOnly, want.IncludeOnly) {
			t.Errorf("duplicate dedup mismatch: LoadProjections=%v config.Load=%v", got, want)
		}
	})

	t.Run("bad .proj.yaml with inline twin: server absent from Projections", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/b.yaml", "name: b\ncommand: echo\nprojections:\n  t:\n    include_only: [inline]\n")
		writeProjLoadFile(t, dir, "servers/b.proj.yaml", "bad: [yaml\n")

		load := config.LoadProjections(dir)

		if _, ok := load.Skipped["b"]; !ok {
			t.Error("malformed b.proj.yaml should cause b to be in Skipped")
		}
		if !load.KeepsPrevious("b") {
			t.Error("KeepsPrevious(b) should be true after proj.yaml failure")
		}
		if _, ok := load.Projections["b"]; ok {
			t.Error("a skipped server must not get partial (inline-only) projections")
		}
	})

	t.Run("bad format: server absent from Projections", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/svc.yaml", "name: svc\ncommand: echo\nprojections:\n  t:\n    format: bad-format\n")

		load := config.LoadProjections(dir)

		if _, ok := load.Skipped["svc"]; !ok {
			t.Error("invalid format should cause svc to be in Skipped")
		}
		if !load.KeepsPrevious("svc") {
			t.Error("KeepsPrevious(svc) should be true after format failure")
		}
		if _, ok := load.Projections["svc"]; ok {
			t.Error("skipped server must be absent from Projections")
		}
	})

	t.Run("malformed servers/b.yaml: unattributable error, a still loads", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/a.yaml", "name: a\ncommand: echo\nprojections:\n  t:\n    include_only: [ok]\n")
		writeProjLoadFile(t, dir, "servers/b.yaml", "bad: [yaml\n")

		load := config.LoadProjections(dir)

		if len(load.SourceErrors) == 0 {
			t.Error("malformed b.yaml should produce a source error")
		}
		if load.Projections["a"]["t"] == nil {
			t.Error("a should still load when b fails")
		}
		if !load.KeepsPrevious("b") {
			t.Error("KeepsPrevious(b) should be true")
		}
		if load.KeepsPrevious("a") {
			t.Error("KeepsPrevious(a) should be false")
		}
	})

	t.Run("invalid name in servers/ file: unattributable, others load", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/good.yaml", "name: good\ncommand: echo\nprojections:\n  t:\n    include_only: [a]\n")
		writeProjLoadFile(t, dir, "servers/bad-name.yaml", "name: invalid name!\ncommand: echo\n")

		load := config.LoadProjections(dir)

		if len(load.SourceErrors) == 0 {
			t.Error("invalid server name should produce a source error")
		}
		if load.Projections["good"]["t"] == nil {
			t.Error("good server should still load when another has invalid name")
		}
		if load.KeepsPrevious("good") {
			t.Error("KeepsPrevious(good) should be false: it loaded successfully")
		}
		if !load.KeepsPrevious("someother") {
			t.Error("KeepsPrevious(someother) should be true: unattributable failure present")
		}
	})

	t.Run("broken config.yaml: source error, inline names keep-previous, file names do not", func(t *testing.T) {
		dir := t.TempDir()
		writeProjLoadFile(t, dir, "servers/file-svc.yaml", "name: file-svc\ncommand: echo\n")
		writeProjLoadFile(t, dir, "config.yaml", "bad: [yaml\n")

		load := config.LoadProjections(dir)

		if len(load.SourceErrors) == 0 {
			t.Error("broken config.yaml should produce a source error")
		}
		if load.SourceErrors[0].Path != filepath.Join(dir, "config.yaml") {
			t.Errorf("expected SourceErrors to contain config.yaml path, got %v", load.SourceErrors)
		}
		if !load.KeepsPrevious("inline-only") {
			t.Error("KeepsPrevious(inline-only) should be true when config.yaml fails")
		}
		if load.KeepsPrevious("file-svc") {
			t.Error("KeepsPrevious(file-svc) should be false: it came from servers/ file")
		}
	})

	t.Run("parity with config.Load for valid multi-server config", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PARITY_TEST_TOKEN", "tok123")
		writeProjLoadFile(t, dir, "servers/a.yaml",
			"name: a\nurl: https://a.example.com\nheaders:\n  Auth: Bearer ${PARITY_TEST_TOKEN}\nprojections:\n  tool1:\n    include_only: [x, y]\n  tool2:\n    exclude: [secret]\n")
		writeProjLoadFile(t, dir, "servers/a.proj.yaml", "tool3:\n  include_only: [z]\n")
		writeProjLoadFile(t, dir, "config.yaml",
			"servers:\n- name: b\n  command: echo\n  projections:\n    toolB:\n      include_only: [q]\n")

		load := config.LoadProjections(dir)
		_, servers, err := config.Load(dir)
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}

		for _, s := range servers {
			lp := load.Projections[s.Name]
			if len(s.Projections) == 0 && len(lp) == 0 {
				continue
			}
			if !reflect.DeepEqual(lp, s.Projections) {
				t.Errorf("parity mismatch for %s:\n  LoadProjections: %v\n  config.Load: %v",
					s.Name, projKeys(lp), projKeys(s.Projections))
			}
		}
	})
}

func projKeys(m map[string]*config.ProjectionConfig) []string {
	return slices.Sorted(maps.Keys(m))
}

func TestLoadProjections_projFilesSourceError(t *testing.T) {
	dir := t.TempDir()
	writeProjLoadFile(t, dir, "servers/svc.yaml", "name: svc\ncommand: echo\n")
	p := filepath.Join(dir, "servers", "svc.proj.yaml")
	if err := os.WriteFile(p, []byte("tool:\n  include_only: [a]\n"), 0000); err != nil {
		t.Skip("cannot create unreadable file:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0600) })
	if os.Getuid() == 0 {
		t.Skip("running as root; permission test not meaningful")
	}

	load := config.LoadProjections(dir)

	if _, ok := load.Skipped["svc"]; !ok {
		t.Error("unreadable .proj.yaml should cause svc to be in Skipped")
	}
	if !load.KeepsPrevious("svc") {
		t.Error("KeepsPrevious(svc) should be true after unreadable proj file")
	}
	if _, ok := load.Projections["svc"]; ok {
		t.Error("svc should be absent from Projections after proj file error")
	}
}
