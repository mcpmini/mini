package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestSaveServerProjection_changesOnlyTheTargetRule(t *testing.T) {
	dir := t.TempDir()
	path := writeServerSource(t, dir, `# work token, not personal
command: mini

env: ["TOKEN=${TOKEN}"]
custom: keep
projections:
  first:
    exclude: [secret]
    alias: public_first
  second: {include_only: [id]}   # keep ids
`)
	before := mustLoadServer(t, dir)

	saved, err := SaveServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: &ProjectionConfig{Exclude: []string{"hidden"}}})

	want := &ProjectionConfig{Alias: "public_first", Exclude: []string{"hidden"}}
	if err != nil || !reflect.DeepEqual(saved, want) {
		t.Fatalf("saved = %+v, %v; want %+v with the alias kept", saved, err, want)
	}
	after := mustLoadServer(t, dir)
	if !reflect.DeepEqual(after.Projections["first"], want) {
		t.Errorf("loaded first = %+v, want %+v", after.Projections["first"], want)
	}
	before.Projections, after.Projections = nil, nil
	if !reflect.DeepEqual(after, before) {
		t.Errorf("the save changed settings outside first:\nbefore %+v\nafter  %+v", before, after)
	}
	data := string(testutil.ReadFile(t, path))
	for _, kept := range []string{"# work token, not personal", "custom: keep", "# keep ids", "include_only: [id]"} {
		if !strings.Contains(data, kept) {
			t.Errorf("saved file lost %q:\n%s", kept, data)
		}
	}
}

func TestSaveServerProjection_rules(t *testing.T) {
	cases := []struct {
		name      string
		file      string
		requested *ProjectionConfig
		want      map[string]*ProjectionConfig
	}{
		{
			name:      "a new rule is added to a file without projections",
			file:      "command: mini\n",
			requested: &ProjectionConfig{Exclude: []string{"secret"}},
			want:      map[string]*ProjectionConfig{"first": {Exclude: []string{"secret"}}},
		},
		{
			name: "deleting an aliased rule keeps only its alias",
			file: "command: mini\nprojections:\n  first: {alias: public_first, exclude: [secret]}\n  second: {exclude: [private]}\n",
			want: map[string]*ProjectionConfig{"first": {Alias: "public_first"}, "second": {Exclude: []string{"private"}}},
		},
		{
			name: "deleting the last rule removes it",
			file: "command: mini\nprojections:\n  first: {exclude: [secret]}\n",
		},
		{
			name:      "empty lists and maps save as no restriction",
			file:      "command: mini\nprojections:\n  first: {include_only: [id], array_limits: {items: 5}}\n",
			requested: &ProjectionConfig{IncludeOnly: []string{}, ArrayLimits: map[string]int{}, Exclude: []string{"secret"}},
			want:      map[string]*ProjectionConfig{"first": {Exclude: []string{"secret"}}},
		},
		{
			name:      "projections written as an alias of this rule alone become a block of their own",
			file:      "command: mini\nrules: &rules {first: {exclude: [secret]}}\nprojections: *rules\n",
			requested: &ProjectionConfig{IncludeOnly: []string{"id"}},
			want:      map[string]*ProjectionConfig{"first": {IncludeOnly: []string{"id"}}},
		},
		{
			name:      "a rule merged into projections keeps its alias when replaced",
			file:      "command: mini\nrules: &rules\n  first: {alias: public_first, exclude: [secret]}\nprojections:\n  <<: *rules\n",
			requested: &ProjectionConfig{Exclude: []string{"hidden"}},
			want:      map[string]*ProjectionConfig{"first": {Alias: "public_first", Exclude: []string{"hidden"}}},
		},
		{
			name: "a rule merged into projections keeps its alias when deleted",
			file: "command: mini\nrules: &rules\n  first: {alias: public_first, exclude: [secret]}\nprojections:\n  <<: *rules\n",
			want: map[string]*ProjectionConfig{"first": {Alias: "public_first"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeServerSource(t, dir, tc.file)

			saved, err := SaveServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: tc.requested})

			if err != nil {
				t.Fatalf("save: %v", err)
			}
			if !reflect.DeepEqual(saved, tc.want["first"]) {
				t.Errorf("saved = %+v, want %+v", saved, tc.want["first"])
			}
			if got := mustLoadServer(t, dir).Projections; len(got)+len(tc.want) > 0 && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("loaded projections = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSaveServerProjection_refusesFilesItCannotEditSafely(t *testing.T) {
	cases := []struct {
		name      string
		file      string
		requested *ProjectionConfig
	}{
		{name: "a file that does not load", file: "command: mini\nprojections:\n  first: {include_only: 5}\n"},
		{name: "a second YAML document", file: "command: mini\n---\ncommand: other\n"},
		{name: "a malformed trailing document", file: "command: mini\n---\nprivate: [broken\n"},
		{name: "projections inherited through a merge key", file: "base: &base\n  projections: {other: {exclude: [secret]}}\n<<: *base\ncommand: mini\n"},
		{name: "projections written as an alias that also holds other rules", file: "command: mini\nrules: &rules {first: {exclude: [secret]}, other: {exclude: [private]}}\nprojections: *rules\n"},
		{name: "a rule another tool shares through an anchor", file: "command: mini\nprojections:\n  first: &rule {exclude: [secret]}\n  other: *rule\n"},
		{name: "a deletion that would expose an inherited rule", file: "command: mini\nbase: &base\n  projections: {first: {exclude: [inherited]}}\n<<: *base\nprojections: {first: {exclude: [direct]}}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeServerSource(t, dir, tc.file)
			requested := &ProjectionConfig{IncludeOnly: []string{"id"}}
			if strings.HasPrefix(tc.name, "a deletion") {
				requested = nil
			}

			_, err := SaveServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: requested})

			if err == nil {
				t.Fatal("save succeeded, want a refusal")
			}
			if got := string(testutil.ReadFile(t, path)); got != tc.file {
				t.Errorf("refused save changed the file:\n%s", got)
			}
		})
	}
}

func TestSaveServerProjection_refusesAServerWithoutAFile(t *testing.T) {
	dir := t.TempDir()

	_, err := SaveServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: &ProjectionConfig{}})

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("save = %v, want not exist", err)
	}
	if _, err := os.Stat(ServerPath(dir, "svc")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("save created the server file: %v", err)
	}
}

func TestSaveServerProjection_writesThroughASymlinkAndKeepsTheFileMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "server.yaml")
	testutil.WriteFile(t, target, "command: echo\n")
	if err := os.Chmod(target, 0640); err != nil {
		t.Fatal(err)
	}
	path := ServerPath(dir, "svc")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if _, err := SaveServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: &ProjectionConfig{Exclude: []string{"secret"}}}); err != nil {
		t.Fatal(err)
	}

	if link, err := os.Lstat(path); err != nil || link.Mode()&os.ModeSymlink == 0 {
		t.Errorf("server path is no longer a symlink: %v, %v", link, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0640 {
		t.Errorf("target mode = %v, %v; want 0640", info, err)
	}
	if mustLoadServer(t, dir).Projections["first"] == nil {
		t.Error("the saved rule did not reach the symlink's target")
	}
}

func TestSaveServerProjection_leavesTheFileAloneWhenTheRuleIsTheSame(t *testing.T) {
	dir := t.TempDir()
	original := "command: echo\n\nprojections:\n  first: {alias: items, exclude: [secret]}   # hand-written\n"
	path := writeServerSource(t, dir, original)
	requested := &ProjectionConfig{Exclude: []string{"secret"}}

	saved, err := SaveServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: requested})

	want := &ProjectionConfig{Alias: "items", Exclude: []string{"secret"}}
	if err != nil || !reflect.DeepEqual(saved, want) {
		t.Fatalf("saved = %+v, %v; want %+v", saved, err, want)
	}
	if after := string(testutil.ReadFile(t, path)); after != original {
		t.Errorf("file was rewritten:\n%s", after)
	}
	if requested.Alias != "" {
		t.Errorf("save changed the caller's rule: alias %q", requested.Alias)
	}
}

func writeServerSource(t *testing.T, dir, content string) string {
	t.Helper()
	path := ServerPath(dir, "svc")
	testutil.WriteFile(t, path, content)
	return path
}

func mustLoadServer(t *testing.T, dir string) ServerConfig {
	t.Helper()
	sc, err := LoadServer(dir, "svc")
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	return sc
}
