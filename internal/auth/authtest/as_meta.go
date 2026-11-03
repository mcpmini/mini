package authtest

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

// ServeASMeta serves OAuth AS metadata JSON at the given path and 404 for everything
// else, until the test ends. Unless meta sets "issuer", it names the issuer its
// well-known path implies, as a correct authorization server does.
func ServeASMeta(t testing.TB, path string, meta map[string]any) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		body := maps.Clone(meta)
		if _, set := body["issuer"]; !set {
			body["issuer"] = srv.URL + issuerPath(path)
		}
		testutil.WriteJSON(w, http.StatusOK, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func issuerPath(metaPath string) string {
	for _, wellKnown := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		if rest, ok := strings.CutPrefix(metaPath, wellKnown); ok {
			return rest
		}
		if prefix, ok := strings.CutSuffix(metaPath, wellKnown); ok {
			return prefix
		}
	}
	return ""
}
