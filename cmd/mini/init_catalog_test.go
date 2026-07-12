package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

func TestParseCatalogSelection(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []int
		err   bool
	}{
		{"empty", "", nil, false},
		{"all", "a", []int{0, 1, 2, 3}, false},
		{"numbers and ranges", "1,3,2-4", []int{0, 2, 1, 3}, false},
		{"out of range rejects all", "1,9", nil, true},
		{"reversed range", "3-1", nil, true},
		{"malformed range", "1-2-3", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCatalogSelection(tt.input, 4)
			if (err != nil) != tt.err || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseCatalogSelection(%q) = %v, %v; want %v, error=%v", tt.input, got, err, tt.want, tt.err)
			}
		})
	}
}

func TestAvailableCatalogEntriesFiltersConfigured(t *testing.T) {
	entries := []catalog.Entry{{Name: "github"}, {Name: "linear"}}
	servers := []config.ServerConfig{{Name: "github"}}
	available := availableCatalogEntries(entries, servers)
	if !reflect.DeepEqual(available, []catalog.Entry{{Name: "linear"}}) {
		t.Errorf("available = %v", available)
	}
}

func TestRunCatalogStepWritesSelectedServerAndProjection(t *testing.T) {
	dir := t.TempDir()
	err := runCatalogStep(catalogStepParams{
		configDir: dir,
		choose:    func(string) string { return "1" },
		out:       &bytes.Buffer{},
		err:       &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var server importers.ServerYAML
	readServerYAML(t, dir, "github", &server)
	if server.Transport != "http" || server.URL != "https://api.githubcopilot.com/mcp/" {
		t.Errorf("server = %+v", server)
	}
	if _, _, err := config.Load(dir); err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "servers", "github.proj.yaml")); err != nil {
		t.Fatalf("github projection: %v", err)
	}
}

func TestCatalogSentryInstallsBundledProjection(t *testing.T) {
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := catalogEntry(t, entries, "sentry")
	if entry.URL != "https://mcp.sentry.dev/mcp" {
		t.Fatalf("sentry URL = %q", entry.URL)
	}
	dir := t.TempDir()
	if err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "servers", "sentry.proj.yaml")); err != nil {
		t.Fatalf("sentry projection: %v", err)
	}
}

func catalogEntry(t *testing.T, entries []catalog.Entry, name string) catalog.Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("catalog entry %q not found", name)
	return catalog.Entry{}
}

func TestSelectCatalogEntriesRepromptsAfterInvalidSelection(t *testing.T) {
	dir := t.TempDir()
	answers := []string{"1,999", "1"}
	errOut := &bytes.Buffer{}
	err := selectCatalogEntries(catalogStepParams{
		configDir: dir,
		choose:    nextCatalogAnswer(&answers),
		err:       errOut,
	}, []catalog.Entry{{Name: "github", URL: "https://api.githubcopilot.com/mcp/"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 0 || !strings.Contains(errOut.String(), "invalid selection") {
		t.Errorf("answers=%v errors=%q", answers, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "servers", "github.yaml")); err != nil {
		t.Fatalf("github server: %v", err)
	}
}

func nextCatalogAnswer(answers *[]string) func(string) string {
	return func(string) string {
		answer := (*answers)[0]
		*answers = (*answers)[1:]
		return answer
	}
}

func TestCatalogOAuthServerReachesAuthPass(t *testing.T) {
	dir := t.TempDir()
	entry := catalog.Entry{Name: "linear", URL: "https://mcp.linear.app/mcp"}
	if err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0}); err != nil {
		t.Fatal(err)
	}
	var authorized []string
	err := runAuthPass(authPassParams{
		configDir: dir,
		choose:    func(string) string { return "a" },
		authorize: recordAuthorization(&authorized, nil),
		out:       &bytes.Buffer{},
		err:       &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authorized, []string{"linear"}) {
		t.Errorf("authorized = %v, want [linear]", authorized)
	}
}

func TestAutoYesSkipsCatalogAndAuth(t *testing.T) {
	dir := authPassConfig(t, "imported")
	called := false
	err := runCatalogStep(catalogStepParams{
		configDir: dir,
		autoYes:   true,
		choose:    func(string) string { called = true; return "a" },
		out:       &bytes.Buffer{},
		err:       &bytes.Buffer{},
	})
	if err != nil || called {
		t.Errorf("runCatalogStep error=%v called=%v", err, called)
	}
	out := &bytes.Buffer{}
	err = runAuthPass(authPassParams{
		configDir: dir,
		autoYes:   true,
		choose:    func(string) string { called = true; return "a" },
		out:       out,
		err:       &bytes.Buffer{},
	})
	if err != nil || called || !strings.Contains(out.String(), "mini auth imported") {
		t.Errorf("runAuthPass error=%v called=%v output=%q", err, called, out.String())
	}
}
