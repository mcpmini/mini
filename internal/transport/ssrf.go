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
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	return validateHost(u.Hostname())
}

func validateHost(host string) error {
	if isPrivateHostname(host) {
		return fmt.Errorf("URL host %q resolves to a private/loopback address", host)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	return validatePrivateIP(host, ip)
}

func isPrivateHostname(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal")
}

// validatePrivateIP checks if ip falls in any ssrfPrivateRanges CIDR block.
// Unmaps IPv4-in-IPv6 (e.g. ::ffff:127.0.0.1) before checking.
func validatePrivateIP(host string, ip net.IP) error {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, cidr := range ssrfPrivateRanges {
		if cidr.Contains(ip) {
			return fmt.Errorf("URL host %q resolves to a private/loopback address", host)
		}
	}
	return nil
}

// SSRFSafeDialer returns a DialContext that resolves hostnames before connecting
// and rejects any address that resolves to a private/loopback IP. This closes
// the DNS rebinding gap in ValidateURL: a hostname that passes validation at
// add_server time could later resolve to a private IP if the attacker controls DNS.
// Dials using the first validated IP directly to prevent TOCTOU.
func SSRFSafeDialer() func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); ip != nil {
			if err := validatePrivateIP(host, ip); err != nil {
				return nil, fmt.Errorf("connection blocked: %w", err)
			}
			return d.DialContext(ctx, network, addr)
		}
		safe, err := resolveSSRFSafe(ctx, host, port)
		if err != nil {
			return nil, err
		}
		return d.DialContext(ctx, network, safe)
	}
}

// resolveSSRFSafe resolves host, validates all returned IPs are non-private,
// and returns the first IP as a host:port string for dialing.
func resolveSSRFSafe(ctx context.Context, host, port string) (string, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no addresses resolved for %s", host)
	}
	for _, a := range ips {
		if err := validatePrivateIP(a.IP.String(), a.IP); err != nil {
			return "", fmt.Errorf("connection to %s blocked (resolved to %s): %w", host, a.IP, err)
		}
	}
	return net.JoinHostPort(ips[0].IP.String(), port), nil
}

var ssrfPrivateRanges = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8", "127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"169.254.0.0/16", "100.64.0.0/10",
		"::1/128", "fc00::/7", "fe80::/10",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, _ := net.ParseCIDR(c)
		nets = append(nets, n)
	}
	return nets
}()
