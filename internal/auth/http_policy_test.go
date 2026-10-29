package auth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func TestPrivateServerURL(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"http://127.0.0.1/mcp", true},
		{"https://10.1.2.3/mcp", true},
		{"http://172.16.0.1", true},
		{"http://192.168.2.1", true},
		{"http://[::1]", true},
		{"http://[fd00::1]", true},
		{"http://LOCALHOST.", true},
		{"http://8.8.8.8", false},
		{"http://100.63.255.255", false},
		{"http://100.64.0.0", true},
		{"http://100.127.255.255", true},
		{"http://100.128.0.0", false},
		{"http://169.254.1.1", false},
		{"http://[fe80::1]", false},
		{"http://0.0.0.0", false},
		{"http://224.0.0.1", false},
		{"http://[::]", false},
		{"http://[ff02::1]", false},
		{"file://localhost/a", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			if got := (newOAuthHTTPFlow(context.Background(), config.ServerConfig{URL: tc.url}).origin != nil); got != tc.want {
				t.Fatalf("private = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrivateServerDNS(t *testing.T) {
	original := lookupAuthHost
	t.Cleanup(func() { lookupAuthHost = original })
	for _, tc := range []struct {
		name      string
		addresses []net.IPAddr
		err       error
		want      bool
	}{
		{name: "private", addresses: []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("::1")}}, want: true},
		{name: "public", addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}},
		{name: "mixed", addresses: []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}, {IP: net.ParseIP("8.8.8.8")}}},
		{name: "empty"},
		{name: "empty address", addresses: []net.IPAddr{{}}},
		{name: "error", err: errors.New("DNS failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookupAuthHost = func(ctx context.Context, host string) ([]net.IPAddr, error) {
				if host != "localhost.attacker.example" {
					t.Fatalf("host = %q", host)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Fatal("lookup deadline missing")
				}
				return tc.addresses, tc.err
			}
			if got := (newOAuthHTTPFlow(context.Background(), config.ServerConfig{URL: "http://localhost.attacker.example/mcp"}).origin != nil); got != tc.want {
				t.Fatalf("private = %v", got)
			}
		})
	}
}

func TestPrivateServerDNSCancellation(t *testing.T) {
	original := lookupAuthHost
	t.Cleanup(func() { lookupAuthHost = original })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lookupAuthHost = func(ctx context.Context, _ string) ([]net.IPAddr, error) { <-ctx.Done(); return nil, ctx.Err() }
	if (newOAuthHTTPFlow(ctx, config.ServerConfig{URL: "http://internal.example"}).origin != nil) {
		t.Fatal("cancelled lookup trusted")
	}
}

type publicMetadataTransport struct {
	base     http.RoundTripper
	metadata string
}

func (p publicMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "8.8.8.8" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(p.metadata)),
			Request:    req,
		}, nil
	}
	return p.base.RoundTrip(req)
}

func TestPublicMCPPrivateOAuthDenied(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusOK) },
		),
	)
	defer target.Close()
	original := noRedirectClient
	t.Cleanup(func() { noRedirectClient = original })
	strict := newAuthHTTPClient()
	strict.Transport = publicMetadataTransport{
		base:     strict.Transport,
		metadata: `{"authorization_servers":["` + target.URL + `"]}`,
	}
	noRedirectClient = strict
	sc := config.ServerConfig{URL: "https://8.8.8.8/mcp", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
	if _, err := newOAuthHTTPFlow(
		context.Background(),
		sc,
	).discoverAndApply(context.Background(), discoveryParams{ServerURL: sc.URL, Auth: sc.Auth}); err == nil {
		t.Fatal("public MCP reached private OAuth metadata")
	}
	if requests.Load() != 0 {
		t.Fatalf("private OAuth received %d requests", requests.Load())
	}
}
