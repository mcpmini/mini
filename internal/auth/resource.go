package auth

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/mcpmini/mini/internal/config"
)

func ApplyResourceURL(sc *config.ServerConfig) error {
	resourceURL, err := canonicalResourceURI(sc.URL)
	if err != nil {
		return err
	}
	sc.Auth.ResourceURL = resourceURL
	return nil
}

func canonicalResourceURI(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse resource URI: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("resource URI must be an absolute HTTP URL")
	}
	canonicalizeResourceAuthority(u)
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == "/" && (u.RawPath == "" || u.RawPath == "/") {
		u.Path, u.RawPath = "", ""
	}
	return u.String(), nil
}

func canonicalizeResourceAuthority(u *url.URL) {
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.User = nil
}
