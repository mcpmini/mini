//go:build test

package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestLoad(t *testing.T) {
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38", len(entries))
	}
}

func TestParseRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"bad name", "schema_version: 1\nentries:\n  - name: bad name\n    url: https://example.com\n    description: test\n    category: test\n", "bad name"},
		{"http url", "schema_version: 1\nentries:\n  - name: example\n    url: http://example.com\n    description: test\n    category: test\n", "example"},
		{"missing description", "schema_version: 1\nentries:\n  - name: example\n    url: https://example.com\n    category: test\n", "example"},
		{"blank category", "schema_version: 1\nentries:\n  - name: example\n    url: https://example.com\n    description: test\n    category: ' '\n", "example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parse error = %v, want entry %q", err, tt.want)
			}
		})
	}
}

var epoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func testConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0700); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	return dir
}

func bufLogger() (*bytes.Buffer, *slog.Logger) {
	buf := &bytes.Buffer{}
	return buf, slog.New(slog.NewTextHandler(buf, nil))
}

func singleEntryJSON() []byte {
	data, _ := json.Marshal(jsonDocument{
		SchemaVersion: 1,
		Entries:       []Entry{{Name: "test-server", URL: "https://example.com", Description: "test server", Category: "test"}},
	})
	return data
}

func writeCacheFile(t *testing.T, dir string, data []byte, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, "internal", "catalog.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return path
}

func tlsResolveParams(ts *httptest.Server, dir string, clk clock.Clock, logger *slog.Logger) ResolveParams {
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return ResolveParams{
		Clock:      clk,
		Client:     client,
		CatalogURL: ts.URL,
		ConfigDir:  dir,
		Logger:     logger,
	}
}

func TestResolveHappyFetchReplacesEmbedded(t *testing.T) {
	dir := testConfigDir(t)
	_, logger := bufLogger()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(singleEntryJSON()) //nolint:errcheck
	}))
	defer ts.Close()

	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clock.NewFakeAt(epoch), logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "test-server" {
		t.Errorf("entries = %v, want single test-server entry", entries)
	}
}

func TestResolveOversizedBodyRejected(t *testing.T) {
	dir := testConfigDir(t)
	logBuf, logger := bufLogger()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), maxFetchBytes+2)) //nolint:errcheck
	}))
	defer ts.Close()

	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clock.NewFakeAt(epoch), logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38 (embedded fallback)", len(entries))
	}
	if !strings.Contains(logBuf.String(), "catalog fetch failed") {
		t.Errorf("expected WARN in log, got %q", logBuf.String())
	}
}

func TestResolveRedirectRefused(t *testing.T) {
	dir := testConfigDir(t)
	logBuf, logger := bufLogger()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.String()+"/redirected", http.StatusMovedPermanently)
	}))
	defer ts.Close()

	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clock.NewFakeAt(epoch), logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38 (embedded fallback)", len(entries))
	}
	if !strings.Contains(logBuf.String(), "catalog fetch failed") {
		t.Errorf("expected WARN in log, got %q", logBuf.String())
	}
}

func TestResolveNonHTTPSURLFallsBackToEmbedded(t *testing.T) {
	dir := testConfigDir(t)
	logBuf, logger := bufLogger()
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Write(singleEntryJSON()) //nolint:errcheck
	}))
	defer ts.Close()

	p := ResolveParams{
		Clock:      clock.NewFakeAt(epoch),
		Client:     ts.Client(),
		CatalogURL: ts.URL, // http:// — rejected before any network request
		ConfigDir:  dir,
		Logger:     logger,
	}
	entries, err := Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38 (embedded fallback)", len(entries))
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("requests = %d, want 0 (https check must prevent request)", n)
	}
	if !strings.Contains(logBuf.String(), "catalog fetch failed") {
		t.Errorf("expected WARN in log, got %q", logBuf.String())
	}
}

func TestResolveSchemaVersion2RejectedFallsBack(t *testing.T) {
	dir := testConfigDir(t)
	logBuf, logger := bufLogger()
	doc, _ := json.Marshal(jsonDocument{SchemaVersion: 2, Entries: []Entry{{Name: "x", URL: "https://x.com", Description: "x", Category: "x"}}})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(doc) //nolint:errcheck
	}))
	defer ts.Close()

	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clock.NewFakeAt(epoch), logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38 (embedded fallback)", len(entries))
	}
	if !strings.Contains(logBuf.String(), "catalog fetch failed") {
		t.Errorf("expected WARN in log, got %q", logBuf.String())
	}
}

func TestResolveInvalidEntryRejectsWholeDoc(t *testing.T) {
	dir := testConfigDir(t)
	logBuf, logger := bufLogger()
	doc, _ := json.Marshal(jsonDocument{SchemaVersion: 1, Entries: []Entry{
		{Name: "good", URL: "https://good.com", Description: "good", Category: "test"},
		{Name: "bad", URL: "http://insecure.com", Description: "bad", Category: "test"},
	}})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(doc) //nolint:errcheck
	}))
	defer ts.Close()

	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clock.NewFakeAt(epoch), logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38 (embedded fallback)", len(entries))
	}
	if !strings.Contains(logBuf.String(), "catalog fetch failed") {
		t.Errorf("expected WARN in log, got %q", logBuf.String())
	}
}

func TestResolveCacheHitWithinTTLZeroRequests(t *testing.T) {
	dir := testConfigDir(t)
	writeCacheFile(t, dir, singleEntryJSON(), epoch)
	_, logger := bufLogger()

	var requests atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Write(singleEntryJSON()) //nolint:errcheck
	}))
	defer ts.Close()

	// Fake clock exactly at epoch: cache age = 0 < 24h.
	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clock.NewFakeAt(epoch), logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("entries = %d, want 1 (from cache)", len(entries))
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("requests = %d, want 0 (cache hit should skip network)", n)
	}
}

func TestResolveExpiredCacheRefetches(t *testing.T) {
	dir := testConfigDir(t)
	// Cache contains one entry; server returns two entries; expired cache should be bypassed.
	twoEntryDoc, _ := json.Marshal(jsonDocument{SchemaVersion: 1, Entries: []Entry{
		{Name: "a", URL: "https://a.com", Description: "a", Category: "test"},
		{Name: "b", URL: "https://b.com", Description: "b", Category: "test"},
	}})
	writeCacheFile(t, dir, singleEntryJSON(), epoch)
	_, logger := bufLogger()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(twoEntryDoc) //nolint:errcheck
	}))
	defer ts.Close()

	// Clock is 25h after epoch: cache is expired.
	clk := clock.NewFakeAt(epoch.Add(25 * time.Hour))
	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clk, logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("entries = %d, want 2 (refetched)", len(entries))
	}
}

func TestResolveFetchFailureWithStaleCacheUsesStalecache(t *testing.T) {
	dir := testConfigDir(t)
	writeCacheFile(t, dir, singleEntryJSON(), epoch)
	_, logger := bufLogger()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	// Clock is 25h after epoch: cache is expired, but fetch fails → stale cache wins.
	clk := clock.NewFakeAt(epoch.Add(25 * time.Hour))
	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clk, logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "test-server" {
		t.Errorf("entries = %v, want stale cache entry", entries)
	}
}

func TestResolveFetchFailureWithCorruptCacheUsesEmbeddedAndWarns(t *testing.T) {
	dir := testConfigDir(t)
	writeCacheFile(t, dir, []byte("not valid json {{{{"), epoch)
	logBuf, logger := bufLogger()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	clk := clock.NewFakeAt(epoch.Add(25 * time.Hour))
	entries, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clk, logger))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38 (embedded fallback)", len(entries))
	}
	if !strings.Contains(logBuf.String(), "catalog fetch failed") {
		t.Errorf("expected WARN in log, got %q", logBuf.String())
	}
}

func TestResolveCacheWrittenWith0600(t *testing.T) {
	dir := testConfigDir(t)
	_, logger := bufLogger()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(singleEntryJSON()) //nolint:errcheck
	}))
	defer ts.Close()

	_, err := Resolve(context.Background(), tlsResolveParams(ts, dir, clock.NewFakeAt(epoch), logger))
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(dir, "internal", "catalog.json")
	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("cache mode = %04o, want 0600", perm)
	}
}
