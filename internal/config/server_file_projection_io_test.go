package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/fileio"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestReplaceServerProjectionPreservesSymlinkAndTargetMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "server.yaml")
	testutil.WriteFile(t, target, "# linked server\ncommand: echo\n")
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
	_, err := ReplaceServerProjection(ServerProjectionParams{
		ConfigDir: dir, ServerName: "svc", Tool: "list", Projection: &ProjectionConfig{Exclude: []string{"secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	link, err := os.Lstat(path)
	if err != nil || link.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("server path is no longer a symlink: %v, %v", link, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("target mode changed: %v, %v", info, err)
	}
	loaded, err := LoadServer(dir, "svc")
	if err != nil || loaded.Projections["list"] == nil {
		t.Fatalf("saved linked projection = %#v, %v", loaded.Projections, err)
	}
}

func TestReplaceServerProjectionDoesNotCreateMissingServer(t *testing.T) {
	dir := t.TempDir()
	_, err := ReplaceServerProjection(ServerProjectionParams{
		ConfigDir: dir, ServerName: "svc", Tool: "list", Projection: &ProjectionConfig{Exclude: []string{"secret"}},
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing server error = %v, want not exist", err)
	}
	if _, err := os.Stat(ServerPath(dir, "svc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing server was created: %v", err)
	}
}

func TestReplaceServerProjectionFailureKeepsOriginalBytes(t *testing.T) {
	dir := t.TempDir()
	path := ServerPath(dir, "svc")
	original := "# user's file\ncommand: echo\nprojections: {list: {exclude: [secret]}}\n"
	testutil.WriteFile(t, path, original)
	failure := errors.New("replacement failed")
	calls := 0
	_, err := replaceServerProjection(replaceServerProjectionParams{
		request: ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "list", Projection: &ProjectionConfig{IncludeOnly: []string{"id"}}},
		replace: func(_ string, _ []byte, _ fileio.ReplaceOptions) error {
			calls++
			return failure
		},
	})
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("replacement = %v after %d calls, want original failure without retry", err, calls)
	}
	if got := string(testutil.ReadFile(t, path)); got != original {
		t.Fatalf("failed save changed the file:\n%s", got)
	}
}

func TestReplaceServerProjectionExhaustionKeepsLastExternalEdit(t *testing.T) {
	dir := t.TempDir()
	path := ServerPath(dir, "svc")
	testutil.WriteFile(t, path, "command: echo\n")
	calls := 0
	lastExternal := ""
	_, err := replaceServerProjection(replaceServerProjectionParams{
		request: ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "list", Projection: &ProjectionConfig{IncludeOnly: []string{"id"}}},
		replace: func(target string, data []byte, opts fileio.ReplaceOptions) error {
			calls++
			lastExternal = fmt.Sprintf("command: echo\n# external edit %d\n", calls)
			testutil.WriteFile(t, target, lastExternal)
			return fileio.ReplaceFile(target, data, opts)
		},
	})
	if err == nil || calls != 3 {
		t.Fatalf("save = %v after %d attempts, want exhaustion after three", err, calls)
	}
	if got := string(testutil.ReadFile(t, path)); got != lastExternal {
		t.Fatalf("exhausted save lost the last external edit:\n%s", got)
	}
	entries, readErr := os.ReadDir(filepath.Dir(path))
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "svc.yaml" {
		t.Fatalf("save left temporary files: %v, %v", entries, readErr)
	}
}

func TestReplaceServerProjectionDoesNotRecreateServerRemovedBeforeRename(t *testing.T) {
	dir := t.TempDir()
	path := ServerPath(dir, "svc")
	testutil.WriteFile(t, path, "command: echo\n")
	_, err := replaceServerProjection(replaceServerProjectionParams{
		request: ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "list", Projection: &ProjectionConfig{IncludeOnly: []string{"id"}}},
		replace: func(target string, data []byte, opts fileio.ReplaceOptions) error {
			if removeErr := os.Remove(target); removeErr != nil {
				t.Fatal(removeErr)
			}
			return fileio.ReplaceFile(target, data, opts)
		},
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("save after removal = %v, want not exist", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("save recreated the removed server: %v", err)
	}
	entries, readErr := os.ReadDir(filepath.Dir(path))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("save left temporary files: %v, %v", entries, readErr)
	}
}

func TestReplaceServerProjectionRefusesBrokenReferencesAndTrailingDocuments(t *testing.T) {
	cases := map[string]string{
		"shared tool anchor":                "command: echo\nprojections:\n  list: &rule {exclude: [secret]}\n  other: *rule\n",
		"aliased block":                     "command: echo\nrules: &rules {list: {exclude: [secret]}}\nprojections: *rules\n",
		"malformed trailing document":       "command: echo\n---\nprivate: [broken\n",
		"deletion exposes inherited target": "command: echo\nbase: &base\n  projections: {list: {exclude: [inherited]}}\n<<: *base\nprojections: {list: {exclude: [direct]}}\n",
	}
	for name, original := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := ServerPath(dir, "svc")
			testutil.WriteFile(t, path, original)
			projection := &ProjectionConfig{IncludeOnly: []string{"id"}}
			if name == "deletion exposes inherited target" {
				projection = nil
			}
			_, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "list", Projection: projection})
			if err == nil {
				t.Fatal("unsafe save succeeded")
			}
			if got := string(testutil.ReadFile(t, path)); got != original {
				t.Fatalf("unsafe save changed the file:\n%s", got)
			}
		})
	}
}

func TestReplaceServerProjectionRoundTripsMiniWrittenBytes(t *testing.T) {
	dir := t.TempDir()
	projection := &ProjectionConfig{Alias: "items", Exclude: []string{"secret"}, StringLimits: map[string]int{"body": 500}}
	original, err := yaml.Marshal(ServerConfig{Command: "echo", Projections: map[string]*ProjectionConfig{"list": projection}})
	if err != nil {
		t.Fatal(err)
	}
	path := ServerPath(dir, "svc")
	testutil.WriteFileBytes(t, path, original)
	request := *projection
	request.Alias = ""
	got, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "list", Projection: &request})
	if err != nil || got == nil || got.Alias != projection.Alias {
		t.Fatalf("no-op save = %#v, %v", got, err)
	}
	if after := testutil.ReadFile(t, path); !bytes.Equal(after, original) {
		t.Fatalf("mini-written file changed on identical save:\n%s", after)
	}
	if request.Alias != "" {
		t.Fatalf("save mutated the request alias: %q", request.Alias)
	}
}
