package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestPrivateOAuthRequiresPinnedSourceContact(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var hits atomic.Int32
			target := httptest.NewServer(
				http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(http.StatusOK) },
				),
			)
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
			}))
			defer source.Close()
			flow := newOAuthHTTPFlow(t.Context(), config.ServerConfig{URL: source.URL + "/mcp"})
			defer flow.closeIdleConnections()
			if _, err := flow.doDiscoveryRequest(t.Context(), target.URL); err == nil {
				t.Fatal("private-other request reached transport before source contact")
			}
			if hits.Load() != 0 {
				t.Fatal("pre-source request reached private target")
			}
			resp, err := flow.doDiscoveryRequest(t.Context(), source.URL+"/metadata")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			resp, err = flow.doDiscoveryRequest(t.Context(), target.URL)
			if status == http.StatusFound {
				if err == nil || hits.Load() != 0 {
					t.Fatal("redirect promoted private policy")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if hits.Load() != 1 {
					t.Fatalf("post-source hits=%d", hits.Load())
				}
			}
		})
	}
}

func TestPrivateOAuthForbiddenDestinationsAfterSource(t *testing.T) {
	source := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }),
	)
	defer source.Close()
	flow := newOAuthHTTPFlow(t.Context(), config.ServerConfig{URL: source.URL})
	defer flow.closeIdleConnections()
	resp, err := flow.doDiscoveryRequest(t.Context(), source.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, endpoint := range []string{"http://169.254.169.254", "http://0.0.0.0", "http://224.0.0.1", "http://240.0.0.1", "http://198.18.0.1", "http://[fe80::1]", "http://[::]", "http://[ff02::1]", "http://[64:ff9b::a9fe:a9fe]"} {
		t.Run(endpoint, func(t *testing.T) {
			if err := flow.validateDiscoveredEndpoint(endpoint, "token_endpoint"); err == nil {
				t.Fatal("forbidden endpoint accepted")
			}
			if _, err := flow.doDiscoveryRequest(
				t.Context(),
				endpoint,
			); err == nil ||
				!strings.Contains(err.Error(), "blocked") {
				t.Fatalf("metadata destination: %v", err)
			}
			if _, err := flow.register(
				t.Context(),
				endpoint,
				"http://localhost/callback",
			); err == nil ||
				!strings.Contains(err.Error(), "blocked") {
				t.Fatalf("registration destination: %v", err)
			}
			req, _ := http.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				endpoint,
				strings.NewReader("refresh_token=private"),
			)
			if _, err := flow.client.Do(req); err == nil || !strings.Contains(err.Error(), "blocked") {
				t.Fatalf("token destination: %v", err)
			}
		})
	}
}

type fallbackMetadataTransport struct{}

func (fallbackMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := `{"authorization_servers":["https://public.example"]}`
	if strings.Contains(req.URL.Path, "oauth-authorization-server") {
		body = `{"issuer":"https://public.example","authorization_endpoint":"https://public.example/auth","token_endpoint":"https://public.example/token","code_challenge_methods_supported":["S256"]}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestPrivateOAuthRedirectSourceCanUsePublicFallback(t *testing.T) {
	source := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusFound) }),
	)
	defer source.Close()
	flow := newOAuthHTTPFlow(t.Context(), config.ServerConfig{URL: source.URL + "/mcp"})
	defer flow.closeIdleConnections()
	tr := flow.client.Transport.(oauthFlowTransport)
	tr.strict = fallbackMetadataTransport{}
	flow.client.Transport = tr
	resp, err := flow.doDiscoveryRequest(t.Context(), source.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	meta, err := flow.discover(t.Context(), "https://public.example/mcp")
	if err != nil || meta.TokenURL != "https://public.example/token" || flow.privateReady() {
		t.Fatalf("public fallback=%v err=%v ready=%v", meta, err, flow.privateReady())
	}
}
