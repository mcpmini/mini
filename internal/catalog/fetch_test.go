package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const oneEntryCatalog = `{"schema_version":1,"entries":[{"name":"remote","title":"Remote","url":"https://remote.example/mcp","description":"remote server","category":"Test","auth":"none"}]}`

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

	c, err := Fetch(context.Background(), client, srv.URL+"/catalog/v1.json")

	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(c.Entries) != 1 || c.Entries[0].Name != "remote" {
		t.Errorf("entries = %+v, want the single remote entry", c)
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
		{"keeps the known entries", withEntry(`{"name":"known","title":"Known","url":"https://known.example/mcp","description":"known server","category":"Test","auth":"none"}`), []string{"known"}, ""},
		{"fails when nothing is left", future, nil, "catalog entries are required"},
		{"still validates the kept entries", withEntry(`{"name":"invalid","title":"Invalid","url":"https://invalid.example/mcp","description":"bad\u001btext","category":"Test","auth":"none"}`), nil, "description contains control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, client := catalogServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(tt.document)) //nolint:errcheck
			})

			c, err := Fetch(context.Background(), client, srv.URL)

			var names []string
			for _, entry := range c.Entries {
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
		{"captive portal page", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("<html><body>Sign in to the network</body></html>")) //nolint:errcheck
		}, "parse catalog"},
		{"unknown schema version", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(strings.Replace(oneEntryCatalog, `"schema_version":1`, `"schema_version":2`, 1))) //nolint:errcheck
		}, "schema_version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, client := catalogServer(t, tt.handler)
			c, err := Fetch(context.Background(), client, srv.URL)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Fetch = %d entries, error %v; want error containing %q", len(c.Entries), err, tt.want)
			}
		})
	}
}

func TestFetchFailsOnNetworkErrors(t *testing.T) {
	tests := []struct {
		name   string
		target func(t *testing.T) (*http.Client, string)
		want   string
	}{
		{"server unreachable", func(t *testing.T) (*http.Client, string) {
			srv := httptest.NewTLSServer(http.NotFoundHandler())
			srv.Close()
			return NewFetchClient(), srv.URL
		}, "refused"},
		{"connection dropped mid-body", func(t *testing.T) (*http.Client, string) {
			srv, client := catalogServer(t, closeAfterPartialBody(t))
			return client, srv.URL
		}, "unexpected EOF"},
		{"untrusted certificate", func(t *testing.T) (*http.Client, string) {
			srv := httptest.NewTLSServer(http.NotFoundHandler())
			t.Cleanup(srv.Close)
			return NewFetchClient(), srv.URL
		}, "certificate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, url := tt.target(t)
			_, err := Fetch(context.Background(), client, url)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Fetch error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func closeAfterPartialBody(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		// Closing is what cuts the body short, and it frees the socket even when it reports an error.
		defer conn.Close() //nolint:errcheck
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n" + oneEntryCatalog[:20])
		if err := buf.Flush(); err != nil {
			t.Errorf("write partial body: %v", err)
		}
	}
}

func TestNewFetchClientGivesUpAfterThreeSeconds(t *testing.T) {
	if got := NewFetchClient().Timeout; got != 3*time.Second {
		t.Errorf("timeout = %v, want 3s so a slow network falls back to the built-in catalog quickly", got)
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

func TestFetchPopular_preservesOrderAndSkipsOnlyUnknownAuthIDs(t *testing.T) {
	entries := []map[string]any{
		validEntry(func(e map[string]any) { e["name"] = "first" }),
		validEntry(func(e map[string]any) { e["name"], e["auth"] = "future", "future-kind" }),
		validEntry(func(e map[string]any) { e["name"] = "second" }),
	}
	data := catalogWithPopular(t, []string{"second", "future", "first"}, entries...)
	srv, client := catalogServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(data); err != nil {
			t.Errorf("write catalog: %v", err)
		}
	})
	c, err := Fetch(context.Background(), client, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Popular, []string{"second", "first"}) || len(c.Entries) != 2 || c.Entries[0].Name != "first" || c.Entries[1].Name != "second" {
		t.Fatalf("Fetch = %+v, want popular [second first] and entries [first second]", c)
	}
}

func TestFetchPopular_rejectsUnknownAndRepeatedIDsBeforeSkippingAuth(t *testing.T) {
	entries := []map[string]any{
		validEntry(func(e map[string]any) { e["name"] = "known" }),
		validEntry(func(e map[string]any) { e["name"], e["auth"] = "future", "future-kind" }),
	}
	for _, tt := range []struct {
		name    string
		popular []string
		want    string
	}{
		{"unknown", []string{"missing"}, "not a catalog server"},
		{"duplicate skipped ID", []string{"future", "future"}, "listed twice"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := catalogWithPopular(t, tt.popular, entries...)
			srv, client := catalogServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if _, err := w.Write(data); err != nil {
					t.Errorf("write catalog: %v", err)
				}
			})
			_, err := Fetch(context.Background(), client, srv.URL)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Fetch error = %v, want %q", err, tt.want)
			}
		})
	}
}
