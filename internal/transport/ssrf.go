package transport

import (
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
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("URL host %q resolves to a private/loopback address", host)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	// Unmap IPv4-in-IPv6 (e.g. ::ffff:127.0.0.1) before checking private ranges.
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
