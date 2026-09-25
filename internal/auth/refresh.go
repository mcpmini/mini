package auth

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
	if isOAuthReauthCode(re.ErrorCode) {
		return true
	}
	if re.Response == nil {
		return false
	}
	return re.Response.StatusCode == http.StatusBadRequest ||
		re.Response.StatusCode == http.StatusUnauthorized
}

func isOAuthReauthCode(code string) bool {
	return code == "invalid_grant" || code == "invalid_client" || code == "unauthorized_client"
}
