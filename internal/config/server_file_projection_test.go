package config

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/fileio"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestReplaceServerProjectionEditsOnlyTargetAndKeepsServerText(t *testing.T) {
	dir := t.TempDir()
	path := ServerPath(dir, "svc")
	original := `# server note
command: mini
env: ["TOKEN=${TOKEN}"]
custom: keep
projections:
    first:
        exclude: [secret]
        alias: public_first
    second: {include_only: [id]}
`
	testutil.WriteFileBytes(t, path, []byte(original))

	got, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: &ProjectionConfig{Exclude: []string{"hidden"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Alias != "public_first" || len(got.Exclude) != 1 || got.Exclude[0] != "hidden" {
		t.Fatalf("target projection = %#v", got)
	}
	data := string(testutil.ReadFile(t, path))
	for _, preserved := range []string{"# server note", `env: ["TOKEN=${TOKEN}"]`, "custom: keep", "second:", "include_only: [id]"} {
		if !strings.Contains(data, preserved) {
			t.Errorf("saved server config lost %q:\n%s", preserved, data)
		}
	}
	loaded, err := LoadServer(dir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Projections["second"].IncludeOnly[0] != "id" {
		t.Fatalf("sibling projection = %#v", loaded.Projections["second"])
	}
}

func TestReplaceServerProjectionDeleteKeepsAliasOnlyRule(t *testing.T) {
	dir := t.TempDir()
	path := ServerPath(dir, "svc")
	testutil.WriteFileBytes(t, path, []byte("command: mini\nprojections:\n  first:\n    alias: public_first\n    exclude: [secret]\n  second:\n    exclude: [private]\n"))
	got, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Alias != "public_first" || len(got.Exclude) != 0 {
		t.Fatalf("committed alias rule = %#v", got)
	}
	loaded, err := LoadServer(dir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Projections["first"].Alias != "public_first" || len(loaded.Projections["second"].Exclude) != 1 {
		t.Fatalf("loaded projections = %#v", loaded.Projections)
	}
}

func TestReplaceServerProjectionAllowsWildcardAndWritesNilDeletion(t *testing.T) {
	dir := t.TempDir()
	path := ServerPath(dir, "svc")
	testutil.WriteFileBytes(t, path, []byte("command: mini\nprojections:\n  old: {exclude: [secret]}\n"))
	if _, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "*", Projection: &ProjectionConfig{Exclude: []string{"secret"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "old"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadServer(dir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Projections["old"]; ok {
		t.Fatalf("deleted rule remains: %#v", loaded.Projections)
	}
	if loaded.Projections["*"].Exclude[0] != "secret" {
		t.Fatalf("wildcard projection = %#v", loaded.Projections["*"])
	}
}

func TestReplaceServerProjectionRefusesUnsafeDocumentsAndInheritedRules(t *testing.T) {
	for name, content := range map[string]string{
		"multiple documents":    "command: mini\n---\ncommand: other\n",
		"inherited projections": "base: &base\n  projections: {other: {exclude: [secret]}}\n<<: *base\ncommand: mini\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFileBytes(t, ServerPath(dir, "svc"), []byte(content))
			before := testutil.ReadFile(t, ServerPath(dir, "svc"))
			_, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: &ProjectionConfig{Exclude: []string{"secret"}}})
			if err == nil || !strings.Contains(err.Error(), "session_only") {
				t.Fatalf("error = %v, want session_only guidance", err)
			}
			if after := testutil.ReadFile(t, ServerPath(dir, "svc")); string(after) != string(before) {
				t.Fatalf("file changed after refusal:\n%s", after)
			}
		})
	}
}

func TestReplaceServerProjectionUsesEffectiveAliasFromProjectionMerge(t *testing.T) {
	for name, projection := range map[string]*ProjectionConfig{
		"replacement":  {Exclude: []string{"hidden"}},
		"nil deletion": nil,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := ServerPath(dir, "svc")
			data := "command: echo\nrules: &rules\n  first: {alias: public_first, exclude: [secret]}\nprojections:\n  <<: *rules\n"
			testutil.WriteFileBytes(t, path, []byte(data))
			got, err := ReplaceServerProjection(ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: projection})
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || got.Alias != "public_first" {
				t.Fatalf("effective projection = %#v, want inherited alias", got)
			}
			loaded, err := LoadServer(dir, "svc")
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Projections["first"].Alias != "public_first" {
				t.Fatalf("saved effective projection = %#v", loaded.Projections["first"])
			}
			if projection == nil && len(loaded.Projections["first"].Exclude) != 0 {
				t.Fatalf("nil deletion kept inherited rule fields: %#v", loaded.Projections["first"])
			}
		})
	}
}

func TestReplaceServerProjectionRetriesFreshDiskAlias(t *testing.T) {
	dir := t.TempDir()
	path := ServerPath(dir, "svc")
	testutil.WriteFileBytes(t, path, []byte("command: mini\nprojections:\n  first: {alias: old_name, exclude: [secret]}\n"))
	calls := 0
	replace := func(target string, data []byte, opts fileio.ReplaceOptions) error {
		calls++
		if calls == 1 {
			testutil.WriteFileBytes(t, target, []byte("# external edit\ncommand: mini\nprojections:\n  first: {alias: fresh_name, exclude: [secret]}\n"))
		}
		return fileio.ReplaceFile(target, data, opts)
	}
	got, err := replaceServerProjection(replaceServerProjectionParams{request: ServerProjectionParams{ConfigDir: dir, ServerName: "svc", Tool: "first", Projection: &ProjectionConfig{Exclude: []string{"hidden"}}}, replace: replace})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("replace calls = %d, want 2", calls)
	}
	if got == nil || got.Alias != "fresh_name" {
		t.Fatalf("committed projection = %#v", got)
	}
	data := testutil.ReadFile(t, path)
	if !strings.Contains(string(data), "# external edit") || !strings.Contains(string(data), "fresh_name") {
		t.Fatalf("retry lost external edit or alias:\n%s", data)
	}
}
