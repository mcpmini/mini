package authtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ServeASMeta returns an httptest.Server that serves OAuth AS metadata JSON at
// the given path and 404 for everything else.
func ServeASMeta(t *testing.T, path string, meta map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(meta) //nolint:errcheck
	}))
}
