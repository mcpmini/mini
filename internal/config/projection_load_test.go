package config_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestLoadServerRejectsUnsupportedExcludePaths(t *testing.T) {
	paths := []string{
		"",
		"items[*].desc",
		"items[]",
		"items[0].desc",
		".description",
		"head.",
		"head..description",
		"..description",
		"*",
		"head.*",
		"head | .repo",
		"head/description",
		"head description",
		"café",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			configtest.WriteServer(t, dir, config.ServerConfig{
				Name: "svc", Command: "echo", Projections: map[string]*config.ProjectionConfig{
					"tool": {Exclude: []string{"valid", path}}, "other": {Exclude: []string{"secret"}},
				},
			})
			sc, err := config.LoadServer(dir, "svc")
			if err != nil || sc.Command != "echo" {
				t.Fatalf("server must remain loadable: %+v, %v", sc, err)
			}
			if sc.ProjectionsErr == nil || !strings.Contains(sc.ProjectionsErr.Err.Error(), "invalid exclude path") {
				t.Fatalf("projection error = %v, want actionable exclude path error", sc.ProjectionsErr)
			}
			if sc.Projections != nil {
				t.Errorf("bad projections must be discarded together: %v", sc.Projections)
			}
		})
	}
}

func TestLoadServerAcceptsRootRelativeExcludePaths(t *testing.T) {
	dir := t.TempDir()
	exclude := []string{
		"description",
		"head.repo.description",
		"items",
		"data.items",
		"0.name",
		"_meta.field-1",
		"[].author.avatar_url",
		"items[].author.avatar_url",
		"groups[].members[].avatar_url",
	}
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name: "svc", Command: "echo", Projections: map[string]*config.ProjectionConfig{
			"nil":   nil,
			"empty": {},
			"paths": {Exclude: exclude},
		},
	})
	sc, err := config.LoadServer(dir, "svc")
	if err != nil || sc.ProjectionsErr != nil || len(sc.Projections) != 3 {
		t.Fatalf("LoadServer = %+v, %v; want all supported projections", sc, err)
	}
	if got := sc.Projections["paths"].Exclude; !slices.Equal(got, exclude) {
		t.Errorf("exclude = %q, want %q after a write and reload", got, exclude)
	}
}

func TestLoadServerAcceptsQuotedArrayExcludePathsAsDocumented(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFileBytes(t, config.ServerPath(dir, "gh"), []byte(`command: gh-mcp
projections:
  list_commits:
    exclude:
      - "[].author.avatar_url"
      - "[].committer.avatar_url"
`))
	sc, err := config.LoadServer(dir, "gh")
	if err != nil || sc.ProjectionsErr != nil {
		t.Fatalf("LoadServer = %+v, %v; want the documented quoted form to load", sc, err)
	}
	want := []string{"[].author.avatar_url", "[].committer.avatar_url"}
	if got := sc.Projections["list_commits"].Exclude; !slices.Equal(got, want) {
		t.Errorf("exclude = %q, want %q", got, want)
	}
}

func TestLoadProjectionConfig(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "gh", Command: "gh-mcp"})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "gh",
		Tools: map[string]*config.ProjectionConfig{
			"list_issues": {
				IncludeOnly: []string{"number", "title"},
				ArrayLimits: map[string]int{"labels": 3},
			},
		},
	})
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections
	if proj == nil {
		t.Fatal("expected projections to be loaded")
	}
	if proj["list_issues"] == nil {
		t.Fatal("expected list_issues projection")
	}
	if len(proj["list_issues"].IncludeOnly) != 2 {
		t.Errorf("expected 2 include_only fields, got %v", proj["list_issues"].IncludeOnly)
	}
}

func TestLoadProjectionMerges_dirWinsOverInline(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:    "svc",
		Command: "my-mcp",
		Projections: map[string]*config.ProjectionConfig{
			"my_tool": {
				IncludeOnly: []string{"inline_field"},
			},
		},
	})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "svc",
		Tools: map[string]*config.ProjectionConfig{
			"my_tool": {
				IncludeOnly: []string{"dir_field"},
			},
		},
	})
	sc := mustLoadOneServer(t, dir)
	proj := sc.Projections["my_tool"]
	if proj == nil {
		t.Fatal("expected projection")
		return
	}
	if len(proj.IncludeOnly) != 1 || proj.IncludeOnly[0] != "dir_field" {
		t.Errorf("expected dir projection to win, got include_only=%v", proj.IncludeOnly)
	}
}

func TestLoadProjectionFormat_rejectsMini(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "gh", Command: "gh-mcp"})
	configtest.WriteProjections(t, dir, configtest.ProjectionFile{
		ServerName: "gh",
		Tools: map[string]*config.ProjectionConfig{
			"list_issues": {
				Format: "mini",
			},
		},
	})
	sc, err := config.LoadServer(dir, "gh")
	if err != nil || sc.ProjectionsErr == nil || !strings.Contains(sc.ProjectionsErr.Err.Error(), "toon") {
		t.Fatalf("LoadServer = %+v, %v; want a projection format error naming toon", sc.ProjectionsErr, err)
	}
}

func TestLoadProjection_rejectsUnknownOption(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "gh", Command: "gh-mcp"})
	configtest.WriteRawProjections(
		configtest.WriteRawProjectionsParams{
			T:           t,
			Dir:         dir,
			ServerName:  "gh",
			Projections: "list_issues:\n  include_only: [id]\n  passthrough: [cursor]\n",
		},
	)
	sc, err := config.LoadServer(dir, "gh")
	if err != nil || sc.ProjectionsErr == nil || !strings.Contains(sc.ProjectionsErr.Err.Error(), `"passthrough"`) {
		t.Fatalf("LoadServer = %+v, %v; want a projection error naming the passthrough option", sc.ProjectionsErr, err)
	}
}
