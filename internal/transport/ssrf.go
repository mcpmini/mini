package transport

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateURL blocks SSRF by rejecting non-http(s) schemes, the well-known
// loopback hostnames, and direct references to private/loopback IP ranges.
// Hostname-based references to internal services (e.g. "internal.corp") are
// not blocked here because that would require DNS resolution at validation time.
func ValidateURL(rawURL string) error { return (SSRFPolicy{}).ValidateURL(rawURL) }

// SSRFPolicy always blocks link-local, unspecified, multicast and reserved destinations.
type SSRFPolicy struct {
	AllowPrivateNetworks bool
}

func (p SSRFPolicy) ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	return p.validateHost(u.Hostname())
}

func (p SSRFPolicy) validateHost(host string) error {
	if !p.AllowPrivateNetworks && isPrivateHostname(host) {
		return fmt.Errorf("URL host %q resolves to a private/loopback address", host)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	return p.validateIP(host, ip)
}

func isPrivateHostname(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal")
}

// IsPrivateNetworkIP identifies RFC 1918/4193, loopback and CGNAT addresses.
func IsPrivateNetworkIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || cgnatRange.Contains(ip)
}

var cgnatRange = func() *net.IPNet {
	//nolint:errcheck // a constant CIDR literal cannot fail to parse.
	_, block, _ := net.ParseCIDR("100.64.0.0/10")
	return block
}()

func (p SSRFPolicy) blocksIP(ip net.IP) bool {
	if !p.AllowPrivateNetworks && IsPrivateNetworkIP(ip) {
		return true
	}
	for _, cidr := range ssrfPrivateRanges {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// NAT64 (64:ff9b::/96): To4() does not unmap it, so ip[12:16] bypasses IPv4 range checks without this.
func (p SSRFPolicy) validateIP(host string, ip net.IP) error {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip16 := ip.To16(); ip16 != nil && nat64Prefix.Contains(ip16) {
		if p.blocksIP(net.IP(ip16[12:16])) {
			return fmt.Errorf("URL host %q resolves to a private/loopback address", host)
		}
	}
	if p.blocksIP(ip) {
		return fmt.Errorf("URL host %q resolves to a private/loopback address", host)
	}
	return nil
}

// SSRFSafeDialer returns a DialContext that resolves hostnames before connecting
// and rejects any address that resolves to a private/loopback IP. This closes
// the DNS rebinding gap in ValidateURL: a hostname that passes validation at
// add_server time could later resolve to a private IP if the attacker controls DNS.
// Dials using the first validated IP directly to prevent TOCTOU.
func SSRFSafeDialer() func(context.Context, string, string) (net.Conn, error) {
	return (SSRFPolicy{}).DialContext()
}

// DialContext validates every resolved address and dials an exact validated IP.
func (p SSRFPolicy) DialContext() func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialSSRFSafe(ssrfDialParams{ctx: ctx, dialer: d, network: network, address: addr, policy: p})
	}
}

type ssrfDialParams struct {
	policy  SSRFPolicy
	ctx     context.Context
	dialer  *net.Dialer
	network string
	address string
}

func dialSSRFSafe(p ssrfDialParams) (net.Conn, error) {
	host, port, err := net.SplitHostPort(p.address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if validationErr := p.policy.validateIP(host, ip); validationErr != nil {
			return nil, fmt.Errorf("connection blocked: %w", validationErr)
		}
		return p.dialer.DialContext(p.ctx, p.network, p.address)
	}
	safe, err := p.policy.resolve(p.ctx, host, port)
	if err != nil {
		return nil, err
	}
	return p.dialer.DialContext(p.ctx, p.network, safe)
}

var lookupSSRFHost = net.DefaultResolver.LookupIPAddr

func (p SSRFPolicy) resolve(ctx context.Context, host, port string) (string, error) {
	ips, err := lookupSSRFHost(ctx, host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no addresses resolved for %s", host)
	}
	for _, a := range ips {
		if err := p.validateIP(a.IP.String(), a.IP); err != nil {
			return "", fmt.Errorf("connection to %s blocked (resolved to %s): %w", host, a.IP, err)
		}
	}
	return net.JoinHostPort(ips[0].IP.String(), port), nil
}

var ssrfPrivateRanges = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",
		"169.254.0.0/16",
		"224.0.0.0/4", "198.18.0.0/15", "240.0.0.0/4",
		"::/128", "fe80::/10", "ff00::/8",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		//nolint:errcheck // c is a fixed source literal defining the SSRF private-range policy.
		_, n, _ := net.ParseCIDR(c)
		nets = append(nets, n)
	}
	return nets
}()

var nat64Prefix = func() *net.IPNet {
	//nolint:errcheck // fixed source literal defines the NAT64 translation prefix.
	_, n, _ := net.ParseCIDR("64:ff9b::/96")
	return n
}()
