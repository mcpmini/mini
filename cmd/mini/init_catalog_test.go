package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/auth"
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
	_, err := runCatalogStep(catalogStepParams{
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
	if _, err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0}); err != nil {
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
	_, err := selectCatalogEntries(catalogStepParams{
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
	if _, err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0}); err != nil {
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
	_, err := runCatalogStep(catalogStepParams{
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

func TestCatalogSentryReachesAuthPassWithoutBundledDefault(t *testing.T) {
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := catalogEntry(t, entries, "sentry")
	dir := t.TempDir()
	if _, err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0}); err != nil {
		t.Fatal(err)
	}
	var authorized []string
	if err := runAuthPass(authPassParams{
		configDir: dir,
		choose:    func(string) string { return "a" },
		authorize: recordAuthorization(&authorized, nil),
		out:       &bytes.Buffer{},
		err:       &bytes.Buffer{},
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authorized, []string{"sentry"}) {
		t.Errorf("authorized = %v, want [sentry]", authorized)
	}
}

func TestCatalogSlackPreservesBundledClient(t *testing.T) {
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	entry := catalogEntry(t, entries, "slack")
	dir := t.TempDir()
	if _, err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0}); err != nil {
		t.Fatal(err)
	}
	var written importers.ServerYAML
	readServerYAML(t, dir, "slack", &written)
	if written.Auth != nil {
		t.Errorf("slack.yaml must not carry an auth block; bundled default provides client_id+callback_port, got %+v", written.Auth)
	}
	_, servers, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var slackCfg *config.ServerConfig
	for i := range servers {
		if servers[i].Name == "slack" {
			slackCfg = &servers[i]
			break
		}
	}
	if slackCfg == nil {
		t.Fatal("slack not found after config.Load")
	}
	if slackCfg.Auth == nil || slackCfg.Auth.ClientID == "" {
		t.Errorf("slack auth after Load = %+v, want bundled client with ClientID set", slackCfg.Auth)
	}
}

func TestWriteCatalogEntriesOAuth2SetsAuthBlock(t *testing.T) {
	entry := catalog.Entry{Name: "test-oauth", URL: "https://example.com/mcp", Description: "test", Category: "test", Auth: "oauth2"}
	dir := t.TempDir()
	if _, err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0}); err != nil {
		t.Fatal(err)
	}
	var server importers.ServerYAML
	readServerYAML(t, dir, "test-oauth", &server)
	if server.Auth == nil || server.Auth.Type != config.AuthTypeOAuth2 {
		t.Errorf("server.Auth = %v, want {type: oauth2}", server.Auth)
	}
}

func TestWriteCatalogEntriesTokenNoAuthBlock(t *testing.T) {
	entry := catalog.Entry{Name: "test-token", URL: "https://example.com/mcp", Description: "test", Category: "test", Auth: "token", SetupURL: "https://example.com/tokens"}
	dir := t.TempDir()
	guidance, err := writeCatalogEntries(dir, []catalog.Entry{entry}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	var server importers.ServerYAML
	readServerYAML(t, dir, "test-token", &server)
	if server.Auth != nil {
		t.Errorf("server.Auth = %v, want nil (token entries must not write auth block)", server.Auth)
	}
	if len(guidance) != 1 || guidance[0].Name != "test-token" {
		t.Errorf("guidance = %v, want [{test-token}]", guidance)
	}
}

func TestCatalogGuidanceLines(t *testing.T) {
	t.Run("token prints name and setup_url", func(t *testing.T) {
		out := &bytes.Buffer{}
		entries := []catalog.Entry{{Name: "github", Auth: "token", SetupURL: "https://github.com/settings/personal-access-tokens"}}
		printCatalogGuidance(out, entries)
		got := out.String()
		if !strings.Contains(got, "github") || !strings.Contains(got, "https://github.com/settings/personal-access-tokens") {
			t.Errorf("guidance = %q", got)
		}
	})
	t.Run("oauth2-app prints name and setup_url", func(t *testing.T) {
		out := &bytes.Buffer{}
		entries := []catalog.Entry{{Name: "asana", Auth: "oauth2-app", SetupURL: "https://app.asana.com/0/my-apps"}}
		printCatalogGuidance(out, entries)
		got := out.String()
		if !strings.Contains(got, "asana") || !strings.Contains(got, "https://app.asana.com/0/my-apps") {
			t.Errorf("guidance = %q", got)
		}
		if !strings.Contains(got, "type: oauth2") {
			t.Errorf("guidance missing 'type: oauth2': %q", got)
		}
		if !strings.Contains(got, "client_id") {
			t.Errorf("guidance missing 'client_id': %q", got)
		}
	})
	t.Run("oauth2-app guidance followed literally yields valid oauth2 config", func(t *testing.T) {
		sc := config.ServerConfig{
			Name: "asana",
			Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "x"},
		}
		if err := auth.ValidateOAuthServer("asana", sc); err != nil {
			t.Errorf("ValidateOAuthServer = %v, want nil", err)
		}
	})
	t.Run("oauth2 prints nothing", func(t *testing.T) {
		out := &bytes.Buffer{}
		printCatalogGuidance(out, []catalog.Entry{{Name: "linear", Auth: "oauth2"}})
		if out.Len() != 0 {
			t.Errorf("expected no output for oauth2, got %q", out.String())
		}
	})
	t.Run("none prints nothing", func(t *testing.T) {
		out := &bytes.Buffer{}
		printCatalogGuidance(out, []catalog.Entry{{Name: "aws-knowledge", Auth: "none"}})
		if out.Len() != 0 {
			t.Errorf("expected no output for none, got %q", out.String())
		}
	})
}

func TestCatalogListingSuffixes(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "a", Category: "test", Description: "desc", Auth: "oauth2"},
		{Name: "b", Category: "test", Description: "desc", Auth: "token"},
		{Name: "c", Category: "test", Description: "desc", Auth: "oauth2-app"},
		{Name: "d", Category: "test", Description: "desc", Auth: "none"},
	}
	out := &bytes.Buffer{}
	printCatalogEntries(out, entries)
	got := out.String()
	if !strings.Contains(got, "(oauth)") {
		t.Errorf("missing (oauth) suffix in %q", got)
	}
	if !strings.Contains(got, "(token)") {
		t.Errorf("missing (token) suffix in %q", got)
	}
	if !strings.Contains(got, "(oauth · own app)") {
		t.Errorf("missing (oauth · own app) suffix in %q", got)
	}
	if strings.Contains(got, "d (") {
		t.Errorf("none entry should have no suffix, got %q", got)
	}
}
