package provider

import (
	"errors"
	"net/http"

	"golang.org/x/oauth2"
)

func refreshNeedsReauth(err error) bool {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return false
	}
	switch {
	case isOAuthTransientCode(re.ErrorCode):
		return false
	case isOAuthReauthCode(re.ErrorCode):
		return true
	case re.Response == nil:
		return false
	}
	return re.Response.StatusCode == http.StatusBadRequest ||
		re.Response.StatusCode == http.StatusUnauthorized
}

func isOAuthReauthCode(code string) bool {
	return code == "invalid_grant" || code == "invalid_client" || code == "unauthorized_client"
}

func isOAuthTransientCode(code string) bool {
	return code == "temporarily_unavailable" || code == "server_error"
}
