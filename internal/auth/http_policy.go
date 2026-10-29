package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type oauthHTTPFlow struct {
	origin          *url.URL
	addresses       []net.IPAddr
	client          *http.Client
	sourceContacted atomic.Bool
	owned           []*http.Transport
}

func newAuthHTTPClient() *http.Client {
	return transport.NewNoRedirectClient(
		transport.NoRedirectClientOptions{Timeout: 30 * time.Second, BlockPrivateIPs: true},
	)
}

func newOAuthHTTPFlow(ctx context.Context, sc config.ServerConfig) *oauthHTTPFlow {
	f := &oauthHTTPFlow{client: noRedirectClient}
	if sc.AgentAdded {
		return f
	}
	u, err := url.Parse(sc.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return f
	}
	addresses := privateAuthAddresses(ctx, u.Hostname())
	if len(addresses) == 0 {
		return f
	}
	f.origin, f.addresses = u, addresses
	f.configureClient()
	return f
}

func (f *oauthHTTPFlow) configureClient() {
	f.client = newAuthHTTPClient()
	policy := transport.SSRFPolicy{AllowPrivateNetworks: true}
	pinned := transport.NewHTTPTransport(policy)
	pinned.DialContext = f.dialOrigin
	private := transport.NewHTTPTransport(policy)
	f.owned = []*http.Transport{pinned, private}
	if strict, ok := f.client.Transport.(*http.Transport); ok {
		f.owned = append(f.owned, strict)
	}
	f.client.Transport = oauthFlowTransport{flow: f, pinned: pinned, strict: f.client.Transport, private: private}
}

var lookupAuthHost = net.DefaultResolver.LookupIPAddr

func privateAuthAddresses(ctx context.Context, host string) []net.IPAddr {
	if ip := net.ParseIP(host); ip != nil {
		if transport.IsPrivateNetworkIP(ip) {
			return []net.IPAddr{{IP: ip}}
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addresses, err := lookupAuthHost(lookupCtx, host)
	if err != nil || len(addresses) == 0 {
		return nil
	}
	for _, address := range addresses {
		if !transport.IsPrivateNetworkIP(address.IP) {
			return nil
		}
	}
	return addresses
}

func (f *oauthHTTPFlow) dialOrigin(ctx context.Context, network, address string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range f.addresses {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("dial configured OAuth source: %w", lastErr)
}

func (f *oauthHTTPFlow) sameOrigin(u *url.URL) bool {
	return f.origin != nil && strings.EqualFold(f.origin.Scheme, u.Scheme) &&
		strings.EqualFold(f.origin.Hostname(), u.Hostname()) && originPort(f.origin) == originPort(u)
}

func originPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func (f *oauthHTTPFlow) closeIdleConnections() {
	if f == nil {
		return
	}
	for _, tr := range f.owned {
		tr.CloseIdleConnections()
	}
}

func (f *oauthHTTPFlow) privateReady() bool { return f.origin != nil && f.sourceContacted.Load() }

type oauthFlowTransport struct {
	flow                    *oauthHTTPFlow
	pinned, strict, private http.RoundTripper
}

func (t oauthFlowTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.flow.sameOrigin(req.URL) {
		resp, err := t.pinned.RoundTrip(req)
		if err == nil && (resp.StatusCode < 300 || resp.StatusCode >= 400) {
			t.flow.sourceContacted.Store(true)
		}
		return resp, err
	}
	if t.flow.privateReady() {
		return t.private.RoundTrip(req)
	}
	return t.strict.RoundTrip(req)
}
