package config

import (
	"strings"
)

// MergedHeaders returns the HTTP headers for sc, including injected auth.
func (sc ServerConfig) MergedHeaders() map[string]string {
	headers := make(map[string]string)
	for k, v := range sc.Headers {
		headers[k] = strings.TrimSpace(v)
	}
	if sc.Auth != nil {
		injectAuth(headers, sc.Auth)
	}
	return headers
}

func injectAuth(headers map[string]string, auth *AuthConfig) {
	token := strings.TrimSpace(auth.Token)
	if token == "" {
		return
	}
	if auth.Type == AuthTypeAPIKey {
		headers[auth.HeaderName()] = token
		return
	}
	headers[auth.HeaderName()] = "Bearer " + token
}

func (sc ServerConfig) HasStaticAuthHeader() bool {
	if sc.Auth == nil {
		return false
	}
	for name, value := range sc.MergedHeaders() {
		if strings.EqualFold(name, sc.Auth.HeaderName()) && value != "" {
			return true
		}
	}
	return false
}

func (sc ServerConfig) UsesOAuthLogin() bool {
	return sc.IsHTTPTransport() && sc.Auth != nil && sc.Auth.Type == AuthTypeOAuth2 && !sc.HasStaticAuthHeader()
}
