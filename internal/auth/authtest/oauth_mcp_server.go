package authtest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/testutil"
)

type OAuthMCPServerParams struct {
	AuthServerURL   string
	BlockConnecting bool
	FailConnecting  bool
}

// OAuthMCPServer is an MCP server that needs a token from AuthServerURL: a request without one
// gets a 401 challenge naming its resource metadata, and a request with one reaches a getData tool.
type OAuthMCPServer struct {
	URL string
	// Connecting receives when a request with a token arrives.
	Connecting        <-chan struct{}
	UnblockConnecting func()
}

func NewOAuthMCPServer(t testing.TB, p OAuthMCPServerParams) *OAuthMCPServer {
	t.Helper()
	connecting, unblocked := make(chan struct{}, 1), make(chan struct{})
	m := &OAuthMCPServer{Connecting: connecting, UnblockConnecting: sync.OnceFunc(func() { close(unblocked) })}
	if !p.BlockConnecting {
		m.UnblockConnecting()
	}
	srv := httptest.NewServer(oauthMCPHandler(p, connecting, unblocked))
	t.Cleanup(func() { m.UnblockConnecting(); srv.Close() })
	m.URL = srv.URL
	return m
}

func oauthMCPHandler(p OAuthMCPServerParams, connecting chan<- struct{}, unblocked <-chan struct{}) http.Handler {
	mcp := mcptest.NewHandler(mcptest.Upstream{Tools: []mcptest.Tool{{Name: "getData"}}})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			testutil.WriteJSON(w, http.StatusOK, ProtectedResource{
				Resource: "http://" + r.Host, AuthorizationServers: []string{p.AuthServerURL},
			})
			return
		}
		if r.Header.Get("Authorization") == "" {
			Challenge{ResourceMetadata: "http://" + r.Host + "/.well-known/oauth-protected-resource"}.Write(w)
			return
		}
		select {
		case connecting <- struct{}{}:
		default:
		}
		<-unblocked
		if p.FailConnecting {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mcp.ServeHTTP(w, r)
	})
}
