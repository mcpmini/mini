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

const oneEntryCatalog = `{"schema_version":1,"popular":["remote"],"categories":[{"title":"Test","servers":[{"name":"remote","title":"Remote","url":"https://remote.example/mcp","description":"remote server","auth":"none"}]}]}`

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
	if entries := c.Entries(); len(entries) != 1 || entries[0].Name != "remote" || !slices.Equal(c.Popular, []string{"remote"}) {
		t.Errorf("catalog = %+v, want the single remote entry, popular", c)
	}
}

func TestFetchSkipsEntriesWithUnknownAuth(t *testing.T) {
	future := strings.Replace(oneEntryCatalog, `"auth":"none"`, `"auth":"future-kind"`, 1)
	withEntry := func(entry string) string {
		return strings.Replace(future, `"servers":[`, `"servers":[`+entry+`,`, 1)
	}
	withCategory := func(entry string) string {
		return strings.Replace(future, `"categories":[`, `"categories":[{"title":"Known","servers":[`+entry+`]},`, 1)
	}
	known := `{"name":"known","title":"Known","url":"https://known.example/mcp","description":"known server","auth":"none"}`
	tests := []struct {
		name       string
		document   string
		wantNames  []string
		wantTitles []string
		wantErr    string
	}{
		{"keeps the known entries", withEntry(known), []string{"known"}, []string{"Test"}, ""},
		{"drops a category it empties", withCategory(known), []string{"known"}, []string{"Known"}, ""},
		{"fails when nothing is left", future, nil, nil, "catalog categories are required"},
		{"still validates the kept entries", withEntry(`{"name":"invalid","title":"Invalid","url":"https://invalid.example/mcp","description":"bad\u001btext","auth":"none"}`), nil, nil, "description contains control characters"},
		{"still rejects a category published empty", strings.Replace(withCategory(known), `"categories":[`, `"categories":[{"title":"Empty","servers":[]},`, 1), nil, nil, "has no servers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, client := catalogServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(tt.document)) //nolint:errcheck
			})

			c, err := Fetch(context.Background(), client, srv.URL)

			names, titles := entryNames(c.Entries()), categoryTitles(c)
			if !slices.Equal(names, tt.wantNames) || !slices.Equal(titles, tt.wantTitles) || (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Fetch = %v %v, %v; want %v %v, error containing %q", titles, names, err, tt.wantTitles, tt.wantNames, tt.wantErr)
			}
			if err == nil && len(c.Popular) != 0 {
				t.Errorf("popular = %v, want the skipped server dropped from it", c.Popular)
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
				t.Errorf("Fetch = %d entries, error %v; want error containing %q", len(c.Entries()), err, tt.want)
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
