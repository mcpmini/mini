package main

import (
	"errors"
	"net/http"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func recordAuthorizationFailure(configDir string, sc config.ServerConfig, cause error) {
	challenge, ok := challengeFromError(cause)
	if ok {
		auth.RecordAuthorizationChallenge(configDir, sc, challenge)
	}
}

func challengeFromError(err error) (auth.Challenge, bool) {
	var unauthorized *transport.UnauthorizedError
	var status *transport.HTTPStatusError
	var challenge auth.Challenge
	switch {
	case errors.As(err, &unauthorized):
		challenge.StatusCode, challenge.WWWAuthenticate = http.StatusUnauthorized, unauthorized.WWWAuthenticate
	case errors.As(err, &status):
		challenge.StatusCode, challenge.WWWAuthenticate = status.StatusCode, status.WWWAuthenticate
	default:
		return challenge, false
	}
	return challenge, true
}
