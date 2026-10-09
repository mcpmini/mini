package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/catalog"
)

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

func TestNonBlankNamesDropsSpacesAndEmptyNames(t *testing.T) {
	if got := nonBlankNames(
		[]string{"linear", " notion", "", " "},
	); !reflect.DeepEqual(
		got,
		[]string{"linear", "notion"},
	) {
		t.Errorf("nonBlankNames = %q, want [linear notion]", got)
	}
}

func TestInitRejectsEmptyAddBeforeChangingAnything(t *testing.T) {
	for _, arg := range []string{"--add=", "--add= , "} {
		t.Run(arg, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("CODEX_HOME", "")
			configDir := filepath.Join(t.TempDir(), "config")
			cmd := newInitCmd(&rootOptions{configDir: configDir})
			cmd.SetArgs([]string{arg})

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
		body      string
		wantNames []string
	}{
		{name: "published catalog", status: http.StatusOK, body: published, wantNames: []string{"remote"}},
		{
			name:      "fetch fails",
			status:    http.StatusInternalServerError,
			body:      published,
			wantNames: embeddedCatalogNames(t),
		},
		{
			name:      "published document changed shape",
			status:    http.StatusOK,
			body:      `{"schema_version":2,"servers":{"remote":"https://remote.example/mcp"}}`,
			wantNames: embeddedCatalogNames(t),
		},
		{
			name:      "published document isn't JSON",
			status:    http.StatusOK,
			body:      "<html>",
			wantNames: embeddedCatalogNames(t),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body)) //nolint:errcheck
			}))
			t.Cleanup(srv.Close)

			entries, err := catalogSource{client: srv.Client(), url: srv.URL}.entries()

			if err != nil || !reflect.DeepEqual(catalogNames(entries), tt.wantNames) {
				t.Errorf("entries = %v, %v; want %v", catalogNames(entries), err, tt.wantNames)
			}
		})
	}
}

func embeddedCatalogNames(t *testing.T) []string {
	t.Helper()
	c, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	return catalogNames(c.Entries)
}

func catalogNames(entries []catalog.Entry) []string {
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
