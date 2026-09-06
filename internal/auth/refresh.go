package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/transport"
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

func nextRefreshDelay(err error, backoff *time.Duration, now time.Time) time.Duration {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil {
		h := re.Response.Header.Get("Retry-After")
		if d := transport.ParseRetryAfter(h, now); d >= 0 {
			return d
		}
	}
	d := *backoff
	*backoff *= 2
	return d
}
