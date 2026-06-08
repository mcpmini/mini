//go:build test

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestLoadPipes(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		wantLen  int
		wantErr  string
		wantName string
	}{
		{
			name: "valid pipe",
			files: map[string]string{
				"create_pr.yaml": `
name: create_pr
description: Creates a PR
steps:
  - id: make_pr
    server: github
    tool: create_pull_request
    args:
      title: "{{ inputs.title }}"
`,
			},
			wantLen:  1,
			wantName: "create_pr",
		},
		{
			name: "missing name",
			files: map[string]string{
				"unnamed.yaml": `
description: No name
steps:
  - id: step1
    server: github
    tool: some_tool
`,
			},
			wantLen: 0,
			wantErr: "name is required",
		},
		{
			name: "name mismatch with filename",
			files: map[string]string{
				"correct_name.yaml": `
name: wrong_name
steps:
  - id: step1
    server: github
    tool: some_tool
`,
			},
			wantLen: 0,
			wantErr: "must match filename stem",
		},
		{
			name: "empty steps",
			files: map[string]string{
				"nosteps.yaml": `
name: nosteps
steps: []
`,
			},
			wantLen: 0,
			wantErr: "steps must not be empty",
		},
		{
			name: "duplicate step IDs",
			files: map[string]string{
				"dup.yaml": `
name: dup
steps:
  - id: step1
    server: github
    tool: tool_a
  - id: step1
    server: github
    tool: tool_b
`,
			},
			wantLen: 0,
			wantErr: "duplicate step id",
		},
		{
			name: "set and server mutually exclusive",
			files: map[string]string{
				"collision.yaml": `
name: collision
steps:
  - id: step1
    server: github
    tool: some_tool
    set:
      foo: "bar"
`,
			},
			wantLen: 0,
			wantErr: "mutually exclusive",
		},
		{
			name: "parallel key rejected",
			files: map[string]string{
				"parallel_pipe.yaml": `
name: parallel_pipe
steps:
  - id: step1
    parallel:
      - server: github
        tool: tool_a
`,
			},
			wantLen: 0,
			wantErr: "parallel steps are not supported",
		},
		{
			name: "invalid server name in step",
			files: map[string]string{
				"badserver.yaml": `
name: badserver
steps:
  - id: step1
    server: "bad server name!"
    tool: some_tool
`,
			},
			wantLen: 0,
			wantErr: "invalid server name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pipesDir := filepath.Join(dir, "pipes")
			if err := os.MkdirAll(pipesDir, 0700); err != nil {
				t.Fatal(err)
			}
			for fname, content := range tc.files {
				if err := os.WriteFile(filepath.Join(pipesDir, fname), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			pipes, err := config.LoadPipes(dir)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(pipes) != tc.wantLen {
				t.Fatalf("got %d pipes, want %d", len(pipes), tc.wantLen)
			}
			if tc.wantName != "" && len(pipes) > 0 && pipes[0].Name != tc.wantName {
				t.Fatalf("pipe name = %q, want %q", pipes[0].Name, tc.wantName)
			}
		})
	}
}

func TestLoadPipes_NoPipesDir_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	pipeCfgs, err := config.LoadPipes(dir)
	if err != nil {
		t.Fatalf("unexpected error when pipes dir does not exist: %v", err)
	}
	if len(pipeCfgs) != 0 {
		t.Errorf("expected 0 pipes, got %d", len(pipeCfgs))
	}
}

func TestLoadPipes_MultipleFiles_AllLoaded(t *testing.T) {
	dir := t.TempDir()
	pipesDir := filepath.Join(dir, "pipes")
	if err := os.MkdirAll(pipesDir, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"pipe_a.yaml": `
name: pipe_a
steps:
  - id: s1
    server: gh
    tool: create_pr
`,
		"pipe_b.yaml": `
name: pipe_b
steps:
  - id: s1
    server: slack
    tool: post
`,
	}
	for fname, content := range files {
		if err := os.WriteFile(filepath.Join(pipesDir, fname), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pipeCfgs, err := config.LoadPipes(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pipeCfgs) != 2 {
		t.Errorf("expected 2 pipes, got %d", len(pipeCfgs))
	}
}

func TestLoadPipes_OneInvalid_OtherLoaded(t *testing.T) {
	dir := t.TempDir()
	pipesDir := filepath.Join(dir, "pipes")
	if err := os.MkdirAll(pipesDir, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"good_pipe.yaml": `
name: good_pipe
steps:
  - id: s1
    server: gh
    tool: create_pr
`,
		"bad_pipe.yaml": `
name: wrong_name
steps:
  - id: s1
    server: gh
    tool: some_tool
`,
	}
	for fname, content := range files {
		if err := os.WriteFile(filepath.Join(pipesDir, fname), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pipeCfgs, err := config.LoadPipes(dir)
	if err == nil {
		t.Fatal("expected error for invalid pipe, got nil")
	}
	if len(pipeCfgs) != 1 {
		t.Errorf("expected 1 valid pipe, got %d", len(pipeCfgs))
	}
}

func TestLoadPipes_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	pipesDir := filepath.Join(dir, "pipes")
	if err := os.MkdirAll(pipesDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipesDir, "broken.yaml"), []byte("{{not valid yaml: ["), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadPipes(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoadPipes_MissingTool_Rejected(t *testing.T) {
	dir := t.TempDir()
	pipesDir := filepath.Join(dir, "pipes")
	if err := os.MkdirAll(pipesDir, 0700); err != nil {
		t.Fatal(err)
	}
	content := `
name: notool
steps:
  - id: step1
    server: gh
`
	if err := os.WriteFile(filepath.Join(pipesDir, "notool.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadPipes(dir)
	if err == nil {
		t.Fatal("expected error for step missing tool, got nil")
	}
	if !strings.Contains(err.Error(), "tool is required") {
		t.Errorf("error %q does not contain 'tool is required'", err.Error())
	}
}

func TestEnsurePipesDir_CreatesMissingDir(t *testing.T) {
	dir := t.TempDir()
	pipesDir := filepath.Join(dir, "pipes")
	if err := config.EnsurePipesDir(dir); err != nil {
		t.Fatalf("EnsurePipesDir failed: %v", err)
	}
	if _, err := os.Stat(pipesDir); os.IsNotExist(err) {
		t.Error("pipes dir was not created")
	}
}

func TestEnsurePipesDir_IdempotentOnExisting(t *testing.T) {
	dir := t.TempDir()
	if err := config.EnsurePipesDir(dir); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if err := config.EnsurePipesDir(dir); err != nil {
		t.Fatalf("second call failed: %v", err)
	}
}

func TestIsReservedServerName(t *testing.T) {
	if !config.IsReservedServerName("user") {
		t.Error("expected 'user' to be reserved")
	}
	if config.IsReservedServerName("github") {
		t.Error("expected 'github' to not be reserved")
	}
}

func TestLoadServerConfig_RejectsUserName(t *testing.T) {
	dir := t.TempDir()
	serversDir := filepath.Join(dir, "servers")
	if err := os.MkdirAll(serversDir, 0700); err != nil {
		t.Fatal(err)
	}
	content := `name: user
command: some-server
`
	if err := os.WriteFile(filepath.Join(serversDir, "user.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := config.Load(dir)
	if err == nil {
		t.Fatal("expected error for reserved server name 'user', got nil")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error %q does not contain 'reserved'", err.Error())
	}
}
