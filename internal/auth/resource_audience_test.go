//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func TestBrowserExchangeUsesPublishedParentResource(t *testing.T) {
	auth.UseEphemeralCallbackPort()
	tokens := authtest.NewTokenServer(t)
	var mcp *httptest.Server
	mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/prm" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).
				Encode(map[string]any{"resource": mcp.URL + "/", "authorization_servers": []string{tokens.Srv.URL}})
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+mcp.URL+`/prm"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcp.Close()
	sc := config.ServerConfig{Name: "synthetic", URL: mcp.URL + "/mcp", Auth: tokens.AuthConfig()}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	login, err := auth.BeginLogin(ctx, sc, beginLoginParams(t))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-code")
	saved, err := login.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tokens.Mu.Lock()
	resource := tokens.LastResource
	tokens.Mu.Unlock()
	if resource != mcp.URL+"/" || saved.Resource != resource || saved.ServerURL != sc.URL {
		t.Fatalf(
			"token resource=%q saved audience=%q binding=%q; want published %q and endpoint %q",
			resource,
			saved.Resource,
			saved.ServerURL,
			mcp.URL+"/",
			sc.URL,
		)
	}
}
