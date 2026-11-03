package authtest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

// ProtectedResource is an RFC 9728 protected resource metadata document.
type ProtectedResource struct {
	Resource             string   `json:"resource,omitempty"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

// ServeProtectedResource serves *prm at path and 404 elsewhere, until the test ends. It reads
// *prm on each request, so a test can set Resource once the resource's own URL is known.
func ServeProtectedResource(t testing.TB, path string, prm *ProtectedResource) *httptest.Server {
	t.Helper()
	return testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		testutil.WriteJSON(w, http.StatusOK, prm)
	})
}

// Challenge is the Bearer challenge a protected MCP server sends with a 401.
type Challenge struct {
	ResourceMetadata string
	Scope            string
}

func (c Challenge) Write(w http.ResponseWriter) {
	header := `Bearer resource_metadata="` + c.ResourceMetadata + `"`
	if c.Scope != "" {
		header += `, scope="` + c.Scope + `"`
	}
	w.Header().Set("WWW-Authenticate", header)
	w.WriteHeader(http.StatusUnauthorized)
}

// ServeChallenge answers every request with a 401 carrying c, until the test ends.
func ServeChallenge(t testing.TB, c Challenge) *httptest.Server {
	t.Helper()
	return testutil.Serve(t, func(w http.ResponseWriter, _ *http.Request) { c.Write(w) })
}
