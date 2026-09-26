//go:build test

package provider_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mcpmini/mini/internal/auth/provider"
	"golang.org/x/oauth2"
)

func TestRefreshNeedsReauth_errorKinds_classifyReauthVsTransient(t *testing.T) {
	makeRetrieve := func(code string, status int) error {
		var resp *http.Response
		if status != 0 {
			resp = &http.Response{StatusCode: status}
		}
		return &oauth2.RetrieveError{ErrorCode: code, Response: resp}
	}
	cases := []struct {
		name       string
		err        error
		wantReauth bool
	}{
		{"invalid_grant 400", makeRetrieve("invalid_grant", http.StatusBadRequest), true},
		{"invalid_client 400", makeRetrieve("invalid_client", http.StatusBadRequest), true},
		{"unauthorized_client 400", makeRetrieve("unauthorized_client", http.StatusBadRequest), true},
		{"401 no error code", makeRetrieve("", http.StatusUnauthorized), true},
		{"400 no error code", makeRetrieve("", http.StatusBadRequest), true},
		{"429 rate limit", makeRetrieve("", http.StatusTooManyRequests), false},
		{"503 service unavailable", makeRetrieve("", http.StatusServiceUnavailable), false},
		{"500 server error", makeRetrieve("", http.StatusInternalServerError), false},
		{"invalid_grant with 200", makeRetrieve("invalid_grant", http.StatusOK), true},
		{"invalid_client with 503", makeRetrieve("invalid_client", http.StatusServiceUnavailable), true},
		{"unauthorized_client with 500", makeRetrieve("unauthorized_client", http.StatusInternalServerError), true},
		{"temporarily_unavailable with 400", makeRetrieve("temporarily_unavailable", http.StatusBadRequest), false},
		{"server_error with 400", makeRetrieve("server_error", http.StatusBadRequest), false},
		{"non-retrieve network error", errors.New("connection refused"), false},
		{"context deadline", context.DeadlineExceeded, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := provider.RefreshNeedsReauth(tc.err)
			if got != tc.wantReauth {
				t.Errorf("RefreshNeedsReauth(%v) = %v, want %v", tc.err, got, tc.wantReauth)
			}
		})
	}
}
