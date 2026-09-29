package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
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
