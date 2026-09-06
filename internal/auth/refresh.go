package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

type refreshClass int

const (
	refreshReauth    refreshClass = iota
	refreshTransient refreshClass = iota
	refreshTerminal  refreshClass = iota
)

type refreshAttempt struct {
	attempt int
	backoff time.Duration
}

func classifyRefreshErr(err error) refreshClass {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return refreshTerminal
	}
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return refreshTransient
	}
	return classifyRetrieveError(re)
}

func classifyRetrieveError(re *oauth2.RetrieveError) refreshClass {
	if isOAuthReauthCode(re.ErrorCode) {
		return refreshReauth
	}
	if re.Response == nil {
		return refreshTransient
	}
	switch {
	case re.Response.StatusCode == http.StatusUnauthorized:
		return refreshReauth
	case re.Response.StatusCode == http.StatusTooManyRequests:
		return refreshTransient
	case re.Response.StatusCode >= 500:
		return refreshTransient
	default:
		return refreshTerminal
	}
}

func isOAuthReauthCode(code string) bool {
	return code == "invalid_grant" || code == "invalid_client" || code == "unauthorized_client"
}

// parseRefreshRetryAfter returns the delay from a Retry-After header value. Returns -1
// when the header is absent, unparseable, or refers to a past time (use backoff instead).
// Capped at 60s to prevent a misbehaving server from stalling the provider indefinitely.
func parseRefreshRetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return -1
	}
	const maxDelay = 60 * time.Second
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxDelay)
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := t.Sub(now); d > 0 {
			return min(d, maxDelay)
		}
	}
	return -1
}

func nextRefreshDelay(err error, backoff *time.Duration, now time.Time) time.Duration {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil {
		h := re.Response.Header.Get("Retry-After")
		if d := parseRefreshRetryAfter(h, now); d >= 0 {
			return d
		}
	}
	d := *backoff
	*backoff *= 2
	return d
}
