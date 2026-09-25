//go:build test

package auth

import (
	"net/http"
	"time"
)

func UseLoopbackHTTPClient() {
	noRedirectClient = &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
