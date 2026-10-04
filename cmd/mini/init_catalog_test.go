package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
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
	names := catalogNames(availableCatalogEntries(entries, nil))
	if !reflect.DeepEqual(names, []string{"a", "c", "b"}) {
		t.Errorf("order = %v, want [a c b]", names)
	}
}

func TestResolveCatalogNames(t *testing.T) {
	entries := []catalog.Entry{{Name: "linear"}, {Name: "notion"}}
	t.Run("case insensitive and input order", func(t *testing.T) {
		got, err := resolveCatalogNames(entries, []string{"Notion", "LINEAR"})
		if err != nil || !reflect.DeepEqual(catalogNames(got), []string{"notion", "linear"}) {
			t.Fatalf("resolve = %v, %v", catalogNames(got), err)
		}
	})
	t.Run("duplicates collapse", func(t *testing.T) {
		got, err := resolveCatalogNames(entries, []string{"linear", "LINEAR"})
		if err != nil || !reflect.DeepEqual(catalogNames(got), []string{"linear"}) {
			t.Fatalf("resolve = %v, %v", catalogNames(got), err)
		}
	})
	t.Run("unknown names reject all", func(t *testing.T) {
		got, err := resolveCatalogNames(entries, []string{"nope", "linear", "zzz"})
		if err == nil || err.Error() != "not in the server catalog: nope, zzz" || got != nil {
			t.Fatalf("resolve = %v, %v", got, err)
		}
	})
}

func TestRunCatalogStepAddsRequestedEntriesWithoutPicker(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "first", URL: "https://first.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://first.example/token"},
		{Name: "second", URL: "https://second.example/mcp"},
	}
	for _, autoYes := range []bool{false, true} {
		t.Run(fmt.Sprintf("autoYes_%v", autoYes), func(t *testing.T) {
			dir := t.TempDir()
			out := &bytes.Buffer{}
			if err := runCatalogStep(catalogStepParams{configDir: dir, autoYes: autoYes, requested: entries, loadCatalog: func() ([]catalog.Entry, error) { return entries, nil }, ask: func(string) string { t.Fatal("picker called"); return "" }, out: out, errOut: &bytes.Buffer{}}); err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if _, err := os.Stat(filepath.Join(dir, "servers", entry.Name+".yaml")); err != nil {
					t.Fatalf("server file %s: %v", entry.Name, err)
				}
			}
			if strings.Contains(out.String(), "Available MCP servers:") || !strings.Contains(out.String(), "first needs an access token") {
				t.Fatalf("unexpected output: %s", out.String())
			}
		})
	}
	t.Run("requested entries keep order across categories", func(t *testing.T) {
		dir := t.TempDir()
		entries := []catalog.Entry{
			{Name: "b", Category: "Y", URL: "https://b.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://b.example/token"},
			{Name: "a", Category: "X", URL: "https://a.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://a.example/token"},
			{Name: "c", Category: "Y", URL: "https://c.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://c.example/token"},
		}
		out := &bytes.Buffer{}
		if err := runCatalogStep(catalogStepParams{configDir: dir, requested: entries, out: out, errOut: &bytes.Buffer{}}); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if b, a, c := strings.Index(got, "b needs an access token"), strings.Index(got, "a needs an access token"), strings.Index(got, "c needs an access token"); b < 0 || a < 0 || c < 0 || b >= a || a >= c {
			t.Errorf("setup notes do not follow requested order b, a, c:\n%s", got)
		}
	})
}

func TestRunCatalogStepRequestedConfiguredEntriesArePreserved(t *testing.T) {
	dir := t.TempDir()
	existing := []byte("preserve this file\n")
	path := filepath.Join(dir, "servers", "already.yaml")
	testutil.WriteFileBytes(t, path, existing)
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "configured",
		Transport: "http",
		URL:       "https://configured.example/mcp",
	})
	entries := []catalog.Entry{
		{Name: "same-url", URL: "https://configured.example/mcp"},
		{Name: "already", URL: "https://already.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://already.example/token"},
	}
	out := &bytes.Buffer{}
	if err := runCatalogStep(catalogStepParams{configDir: dir, requested: entries, ask: func(string) string { t.Fatal("picker called"); return "" }, out: out, errOut: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "servers", "same-url.yaml")); !os.IsNotExist(err) {
		t.Fatalf("same URL created second file: %v", err)
	}
	got := testutil.ReadFile(t, path)
	if !bytes.Equal(got, existing) {
		t.Fatalf("existing file = %q", got)
	}
	if !strings.Contains(out.String(), "same-url already configured") || !strings.Contains(out.String(), "already already configured") || strings.Contains(out.String(), "already needs an access token") {
		t.Fatalf("unexpected existing-entry output: %s", out.String())
	}
}

func TestNonBlankNamesDropsSpacesAndEmptyNames(t *testing.T) {
	if got := nonBlankNames([]string{"linear", " notion", "", " "}); !reflect.DeepEqual(got, []string{"linear", "notion"}) {
		t.Errorf("nonBlankNames = %q, want [linear notion]", got)
	}
}

func TestInitRejectsEmptyAddBeforeChangingAnything(t *testing.T) {
	for _, arg := range []string{"--add=", "--add= , "} {
		t.Run(arg, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			configDir := filepath.Join(t.TempDir(), "config")
			cmd := newInitCmd(&rootOptions{configDir: configDir})
			cmd.SetArgs([]string{"--yes", arg})

			err := cmd.Execute()

			if err == nil || !strings.Contains(err.Error(), "at least one server name") {
				t.Fatalf("init %s: error %v, want one asking for a server name", arg, err)
			}
			if _, statErr := os.Stat(configDir); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("config dir after a rejected --add: %v, want it not created", statErr)
			}
		})
	}
}

func TestInitAddFlagAppendsValues(t *testing.T) {
	cmd := newInitCmd(&rootOptions{})
	if err := cmd.ParseFlags([]string{"--add", "a,b", "--add", "c"}); err != nil {
		t.Fatal(err)
	}
	got, err := cmd.Flags().GetStringSlice("add")
	if err != nil || !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("add flag = %v, %v", got, err)
	}
}

func TestCatalogSourcePrefersPublishedCatalog(t *testing.T) {
	published := `{"schema_version":1,"entries":[{"name":"remote","title":"Remote","url":"https://remote.example/mcp","description":"remote","category":"Test","auth":"none"}]}`
	tests := []struct {
		name      string
		status    int
		wantNames []string
		wantNote  bool
	}{
		{"published catalog", http.StatusOK, []string{"remote"}, false},
		{"fetch fails", http.StatusInternalServerError, embeddedCatalogNames(t), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(published)) //nolint:errcheck
			}))
			t.Cleanup(srv.Close)
			var warn bytes.Buffer

			entries, err := catalogSource{client: srv.Client(), url: srv.URL, warn: &warn}.entries()

			if err != nil || !reflect.DeepEqual(catalogNames(entries), tt.wantNames) {
				t.Errorf("entries = %v, %v; want %v", catalogNames(entries), err, tt.wantNames)
			}
			if gotNote := strings.Contains(warn.String(), "built-in server catalog"); gotNote != tt.wantNote {
				t.Errorf("note printed = %v, want %v (%q)", gotNote, tt.wantNote, warn.String())
			}
		})
	}
}

func embeddedCatalogNames(t *testing.T) []string {
	t.Helper()
	entries, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	return catalogNames(entries)
}

func catalogNames(entries []catalog.Entry) []string {
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func TestEntryHostShowsWhereTheServerIs(t *testing.T) {
	for rawURL, want := range map[string]string{
		"https://github.com:evil@x.example/mcp": "x.example",
		"https://a.example:8443/mcp":            "a.example:8443",
	} {
		if got := entryHost(rawURL); got != want {
			t.Errorf("entryHost(%q) = %q, want %q", rawURL, got, want)
		}
	}
}

func TestPrintCatalogEntriesNumbersEntriesUnderCategoryHeaders(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "a", Title: "A", Description: "first", Category: "Dev", URL: "https://a.example/mcp", Auth: catalog.AuthOAuth2},
		{Name: "c", Title: "C", Description: "third", Category: "Dev", URL: "https://c.example/mcp", Auth: catalog.AuthNone},
		{Name: "b", Title: "B", Description: "second", Category: "Data", URL: "https://b.example/mcp", Auth: catalog.AuthToken},
	}
	var out bytes.Buffer
	printCatalogEntries(&out, entries)
	want := "Available MCP servers:\n  Dev:\n    1. A [a.example] - first (OAuth login)\n    2. C [c.example] - third\n  Data:\n    3. B [b.example] - second (needs an access token)\n"
	if out.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestRunCatalogStepNeverReplacesAServerFileThatFailsToLoad(t *testing.T) {
	dir := t.TempDir()
	original := "transport: http\nurl: [unfinished\n"
	path := filepath.Join(dir, "servers", "github.yaml")
	testutil.WriteFile(t, path, original)
	out := &bytes.Buffer{}

	err := runCatalogStep(catalogStepParams{
		configDir:   dir,
		loadCatalog: catalog.Load,
		ask:         func(string) string { return "a" },
		out:         out,
		errOut:      &bytes.Buffer{},
	})

	if err != nil {
		t.Fatal(err)
	}
	if data := testutil.ReadFile(t, path); string(data) != original {
		t.Errorf("servers/github.yaml = %q, want it untouched", data)
	}
	if !strings.Contains(out.String(), "  github already configured in mini") {
		t.Errorf("output does not report the existing servers/github.yaml:\n%s", out.String())
	}
	if strings.Contains(out.String(), "GITHUB_TOKEN") {
		t.Errorf("setup note printed for github, which was not written:\n%s", out.String())
	}
}

func TestRunCatalogStepStillFiltersWhenAServerFileFailsToLoad(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers", "my-linear.yaml"), "transport: http\nurl: https://mcp.linear.app/mcp\nheaders:\n  Authorization: Bearer ${MINI_TEST_UNSET_CATALOG_VAR}\n")
	testutil.WriteFile(t, filepath.Join(dir, "servers", "broken.yaml"), "transport: [broken\n")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

	err := runCatalogStep(catalogStepParams{
		configDir:   dir,
		loadCatalog: catalog.Load,
		ask:         func(string) string { return "" },
		out:         out,
		errOut:      errOut,
	})

	if err != nil {
		t.Fatalf("runCatalogStep: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the login step reports broken files", errOut.String())
	}
	if !strings.Contains(out.String(), "Available MCP servers:") || strings.Contains(out.String(), " Linear [") {
		t.Errorf("catalog should be offered without linear, configured as my-linear:\n%s", out.String())
	}
}

func TestSelectCatalogEntriesPrintsSetupNotesAfterPartialWrite(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "first", URL: "https://first.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://first.example/tokens"},
		{Name: "invalid/name", URL: "https://second.example/mcp"},
	}
	out := &bytes.Buffer{}

	err := selectCatalogEntries(catalogStepParams{
		configDir: t.TempDir(),
		ask:       func(string) string { return "1-2" },
		out:       out,
		errOut:    &bytes.Buffer{},
	}, entries)

	if err == nil {
		t.Fatal("selectCatalogEntries succeeded, want the second server write to fail")
	}
	if !strings.Contains(out.String(), "first needs an access token: create one at https://first.example/tokens") {
		t.Errorf("output missing setup note for the written server:\n%s", out.String())
	}
}

func TestRunCatalogStepWritesSelectedServerAndProjection(t *testing.T) {
	dir := t.TempDir()
	out := &bytes.Buffer{}
	err := runCatalogStep(catalogStepParams{
		configDir:   dir,
		loadCatalog: catalog.Load,
		ask:         func(string) string { return catalogNumberOf(t, out.String(), "GitHub") },
		out:         out,
		errOut:      &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var server config.ServerConfig
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
	if want := "added github → " + filepath.Join(dir, "servers", "github.yaml"); !strings.Contains(out.String(), want) {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestRunCatalogStepReportsAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "servers"), "")
	out := &bytes.Buffer{}

	err := runCatalogStep(catalogStepParams{
		configDir:   dir,
		loadCatalog: catalog.Load,
		ask:         func(string) string { return catalogNumberOf(t, out.String(), "GitHub") },
		out:         out,
		errOut:      &bytes.Buffer{},
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
	if _, err := writeCatalogEntries(catalogStepParams{configDir: dir, out: &bytes.Buffer{}}, []catalog.Entry{entry}, []int{0}); err != nil {
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

func catalogNumberOf(t *testing.T, listing, title string) string {
	t.Helper()
	for _, line := range strings.Split(listing, "\n") {
		number, rest, ok := strings.Cut(strings.TrimSpace(line), ". ")
		if ok && strings.HasPrefix(rest, title+" [") {
			return number
		}
	}
	t.Fatalf("%s not listed:\n%s", title, listing)
	return ""
}

func TestSelectCatalogEntriesRepromptsAfterInvalidSelection(t *testing.T) {
	dir := t.TempDir()
	answers := []string{"1,999", "1"}
	errOut := &bytes.Buffer{}
	err := selectCatalogEntries(catalogStepParams{
		configDir: dir,
		ask:       nextCatalogAnswer(&answers),
		out:       &bytes.Buffer{},
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

func TestSelectCatalogEntriesPrintsSetupNotesForSelectedServers(t *testing.T) {
	entries := []catalog.Entry{
		{Name: "my-svc", URL: "https://svc.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://svc.example/tokens"},
		{Name: "apps", URL: "https://apps.example/mcp", Auth: catalog.AuthOAuth2App, SetupURL: "https://apps.example/new-app"},
		{Name: "managed", URL: "https://managed.example/mcp", Auth: catalog.AuthOAuth2},
		{Name: "unpicked", URL: "https://unpicked.example/mcp", Auth: catalog.AuthToken, SetupURL: "https://unpicked.example/tokens"},
	}
	out := &bytes.Buffer{}

	err := selectCatalogEntries(catalogStepParams{
		configDir: t.TempDir(),
		ask:       func(string) string { return "1-3" },
		out:       out,
		errOut:    &bytes.Buffer{},
	}, entries)

	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"my-svc needs an access token: create one at https://svc.example/tokens",
		"Authorization: Bearer ${MY_SVC_TOKEN}",
		"apps needs your own OAuth app: register one at https://apps.example/new-app with redirect URI " + auth.ResolvedCallbackURI(nil),
		"and run: mini auth apps",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "managed needs") || strings.Contains(out.String(), "unpicked") {
		t.Errorf("notes for servers that need no setup or weren't selected:\n%s", out.String())
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
	if _, err := writeCatalogEntries(catalogStepParams{configDir: dir, out: &bytes.Buffer{}}, entries, []int{0, 1, 2, 3}); err != nil {
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
		configDir:   dir,
		loadCatalog: func() ([]catalog.Entry, error) { called = true; return nil, nil },
		autoYes:     true,
		ask:         func(string) string { called = true; return "a" },
		out:         &bytes.Buffer{},
		errOut:      &bytes.Buffer{},
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
