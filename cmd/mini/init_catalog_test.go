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
		{"zero", "0", nil, true},
		{"range from zero", "0-2", nil, true},
		{"uppercase all", "A", []int{0, 1, 2, 3}, false},
		{"spaces around tokens", " 1 , 3 ", []int{0, 2}, false},
		{"not a number", "x", nil, true},
		{"range start not a number", "x-2", nil, true},
		{"range end not a number", "1-x", nil, true},
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

func TestAvailableCatalogEntriesFiltersConfiguredNamesAndURLs(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "github", URL: "https://github.example/mcp"},
		{Name: "linear", URL: "https://linear.example/mcp"},
		{Name: "notion", URL: "https://notion.example/mcp"},
	}
	servers := []config.ServerConfig{{Name: "GitHub"}, {Name: "my-linear", URL: "https://LINEAR.example/mcp/"}}
	available := availableCatalogEntries(entries, servers)
	if !reflect.DeepEqual(available, entries[2:]) {
		t.Errorf("available = %v, want only notion", available)
	}
}

func TestAvailableCatalogEntriesGroupsCategoriesInFirstSeenOrder(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "a", Category: "Dev"},
		{Name: "b", Category: "Data"},
		{Name: "c", Category: "Dev"},
	}
	var names []string
	for _, entry := range availableCatalogEntries(entries, nil) {
		names = append(names, entry.Name)
	}
	if !reflect.DeepEqual(names, []string{"a", "c", "b"}) {
		t.Errorf("order = %v, want [a c b]", names)
	}
}

func TestPrintCatalogEntriesNumbersEntriesUnderCategoryHeaders(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "a", Description: "first", Category: "Dev"},
		{Name: "c", Description: "third", Category: "Dev"},
		{Name: "b", Description: "second", Category: "Data"},
	}
	var out bytes.Buffer
	printCatalogEntries(&out, entries)
	want := "Available MCP servers:\n  Dev:\n    1. a - first\n    2. c - third\n  Data:\n    3. b - second\n"
	if out.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestRunCatalogStepNeverReplacesAnExistingServerFile(t *testing.T) {
	dir := t.TempDir()
	original := "name: gh\ntransport: http\nurl: https://corp.example/mcp\n"
	path := filepath.Join(dir, "servers", "github.yaml")
	writeLoginStepFile(t, path, original)
	out := &bytes.Buffer{}

	err := runCatalogStep(catalogStepParams{
		configDir: dir,
		ask:       func(string) string { return "a" },
		out:       out,
		errOut:    &bytes.Buffer{},
	})

	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != original { //nolint:errcheck // a missing file fails the comparison
		t.Errorf("servers/github.yaml = %q, want it untouched", data)
	}
	if !strings.Contains(out.String(), "  github already configured in mini") {
		t.Errorf("output does not report the existing servers/github.yaml:\n%s", out.String())
	}
}

func TestRunCatalogStepStillFiltersWhenAServerFileFailsToLoad(t *testing.T) {
	dir := t.TempDir()
	writeLoginStepFile(t, filepath.Join(dir, "servers", "my-linear.yaml"), "name: my-linear\ntransport: http\nurl: https://mcp.linear.app/mcp\nheaders:\n  Authorization: Bearer ${MINI_TEST_UNSET_CATALOG_VAR}\n")
	writeLoginStepFile(t, filepath.Join(dir, "servers", "broken.yaml"), "name: [broken\n")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

	err := runCatalogStep(catalogStepParams{
		configDir: dir,
		ask:       func(string) string { return "" },
		out:       out,
		errOut:    errOut,
	})

	if err != nil {
		t.Fatalf("runCatalogStep: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the login step reports broken files", errOut.String())
	}
	if !strings.Contains(out.String(), "Available MCP servers:") || strings.Contains(out.String(), " linear - ") {
		t.Errorf("catalog should be offered without linear, configured as my-linear:\n%s", out.String())
	}
}

func TestRunCatalogStepWritesSelectedServerAndProjection(t *testing.T) {
	dir := t.TempDir()
	out := &bytes.Buffer{}
	err := runCatalogStep(catalogStepParams{
		configDir: dir,
		ask:       func(string) string { return catalogNumberOf(t, out.String(), "github") },
		out:       out,
		errOut:    &bytes.Buffer{},
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

func TestRunCatalogStepReportsAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "servers"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}

	err := runCatalogStep(catalogStepParams{
		configDir: dir,
		ask:       func(string) string { return catalogNumberOf(t, out.String(), "github") },
		out:       out,
		errOut:    &bytes.Buffer{},
	})

	if err == nil || !strings.Contains(err.Error(), "servers") {
		t.Errorf("runCatalogStep error = %v, want the failed write under servers/", err)
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
	if err := writeCatalogEntries(catalogStepParams{configDir: dir, out: &bytes.Buffer{}}, []catalog.Entry{entry}, []int{0}); err != nil {
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

func catalogNumberOf(t *testing.T, listing, name string) string {
	t.Helper()
	for _, line := range strings.Split(listing, "\n") {
		number, rest, ok := strings.Cut(strings.TrimSpace(line), ". ")
		if ok && strings.HasPrefix(rest, name+" - ") {
			return number
		}
	}
	t.Fatalf("%s not listed:\n%s", name, listing)
	return ""
}

func TestSelectCatalogEntriesRepromptsAfterInvalidSelection(t *testing.T) {
	dir := t.TempDir()
	answers := []string{"1,999", "1"}
	errOut := &bytes.Buffer{}
	err := selectCatalogEntries(catalogStepParams{
		configDir: dir,
		ask:       nextCatalogAnswer(&answers),
		errOut:    errOut,
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

func TestCatalogOAuthEntriesReachLoginStep(t *testing.T) {
	dir := t.TempDir()
	entries := []catalog.Entry{
		{Name: "notion", URL: "https://mcp.notion.com/mcp", Auth: catalog.AuthOAuth2},
		{Name: "slack", URL: "https://mcp.slack.com/mcp", Auth: catalog.AuthOAuth2},
		{Name: "deepwiki", URL: "https://mcp.deepwiki.com/mcp", Auth: catalog.AuthNone},
		{Name: "github", URL: "https://api.githubcopilot.com/mcp/", Auth: catalog.AuthToken},
	}
	if err := writeCatalogEntries(catalogStepParams{configDir: dir, out: &bytes.Buffer{}}, entries, []int{0, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	var authorized []string
	runLoginStep(loginStepParams{
		configDir: dir,
		ask:       func(string) string { return "a" },
		logIn:     recordAuthorization(&authorized, nil),
		out:       &bytes.Buffer{},
		errOut:    &bytes.Buffer{},
	})
	if !reflect.DeepEqual(authorized, []string{"notion", "slack"}) {
		t.Errorf("authorized = %v, want [notion slack]", authorized)
	}
	_, servers, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if slack := config.FindServer(servers, "slack"); slack == nil || slack.Auth == nil || slack.Auth.ClientID == "" {
		t.Errorf("slack auth = %+v, want the bundled client registration", slack)
	}
}

func TestAutoYesSkipsCatalogAndAuth(t *testing.T) {
	dir := loginStepConfig(t, "imported")
	called := false
	err := runCatalogStep(catalogStepParams{
		configDir: dir,
		autoYes:   true,
		ask:       func(string) string { called = true; return "a" },
		out:       &bytes.Buffer{},
		errOut:    &bytes.Buffer{},
	})
	if err != nil || called {
		t.Errorf("runCatalogStep error=%v called=%v", err, called)
	}
	out := &bytes.Buffer{}
	runLoginStep(loginStepParams{
		configDir: dir,
		autoYes:   true,
		ask:       func(string) string { called = true; return "a" },
		out:       out,
		errOut:    &bytes.Buffer{},
	})
	if called || !strings.Contains(out.String(), "mini auth imported") {
		t.Errorf("runLoginStep called=%v output=%q", called, out.String())
	}
}
