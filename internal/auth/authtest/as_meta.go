package authtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		response := make(map[string]any, len(meta)+1)
		for key, value := range meta {
			response[key] = value
		}
		if _, ok := response["issuer"]; !ok {
			response["issuer"] = "http://" + r.Host + issuerPath(path)
		}
		json.NewEncoder(w).Encode(response) //nolint:errcheck
	}))
}

func issuerPath(metadataPath string) string {
	for _, prefix := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/openid-configuration",
	} {
		if strings.HasPrefix(metadataPath, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(metadataPath, prefix), "/")
		}
		if strings.HasSuffix(metadataPath, prefix) {
			return strings.TrimSuffix(metadataPath, prefix)
		}
	}
	return ""
}
