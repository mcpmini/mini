package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const oneEntryCatalog = `{"schema_version":1,"entries":[{"name":"remote","url":"https://remote.example/mcp","description":"remote server","category":"Test","auth":"none"}]}`

func catalogServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	client := NewFetchClient()
	client.Transport = srv.Client().Transport
	return srv, client
}

func TestFetchReturnsPublishedEntries(t *testing.T) {
	srv, client := catalogServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(oneEntryCatalog)) //nolint:errcheck
	})

	entries, err := Fetch(context.Background(), client, srv.URL+"/catalog/v1.json")

	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "remote" {
		t.Errorf("entries = %+v, want the single remote entry", entries)
	}
}

func TestFetchSkipsEntriesWithUnknownAuth(t *testing.T) {
	future := strings.Replace(oneEntryCatalog, `"auth":"none"`, `"auth":"future-kind"`, 1)
	withEntry := func(entry string) string {
		return strings.Replace(future, `"entries":[`, `"entries":[`+entry+`,`, 1)
	}
	tests := []struct {
		name      string
		document  string
		wantNames []string
		wantErr   string
	}{
		{"keeps the known entries", withEntry(`{"name":"known","url":"https://known.example/mcp","description":"known server","category":"Test","auth":"none"}`), []string{"known"}, ""},
		{"fails when nothing is left", future, nil, "catalog entries are required"},
		{"still validates the kept entries", withEntry(`{"name":"invalid","url":"https://invalid.example/mcp","description":"bad\u001btext","category":"Test","auth":"none"}`), nil, "description contains control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, client := catalogServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(tt.document)) //nolint:errcheck
			})

			entries, err := Fetch(context.Background(), client, srv.URL)

			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name)
			}
			if !slices.Equal(names, tt.wantNames) || (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Fetch = %v, %v; want %v, error containing %q", names, err, tt.wantNames, tt.wantErr)
			}
		})
	}
}

func TestFetchRejectsUnusableResponses(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"error status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }, "status 404"},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.example/catalog.json", http.StatusFound)
		}, "status 302"},
		{"oversized body", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(strings.Repeat(" ", maxFetchBytes+1))) //nolint:errcheck
		}, "exceeds"},
		{"invalid entry", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(strings.Replace(oneEntryCatalog, "https://remote", "http://remote", 1))) //nolint:errcheck
		}, "https"},
		{"unknown schema version", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(strings.Replace(oneEntryCatalog, `"schema_version":1`, `"schema_version":2`, 1))) //nolint:errcheck
		}, "schema_version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, client := catalogServer(t, tt.handler)
			entries, err := Fetch(context.Background(), client, srv.URL)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Fetch = %d entries, error %v; want error containing %q", len(entries), err, tt.want)
			}
		})
	}
}

func TestFetchRefusesNonHTTPSURLWithoutRequesting(t *testing.T) {
	requested := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requested = true }))
	t.Cleanup(srv.Close)

	_, err := Fetch(context.Background(), NewFetchClient(), srv.URL)

	if err == nil || requested {
		t.Errorf("Fetch over http: error %v, requested %v; want an error and no request", err, requested)
	}
}
