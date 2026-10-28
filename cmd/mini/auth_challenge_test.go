//go:build test

package main

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/transport"
)

func TestCLIRecordsScopeFailureForExplicitReauthorization(t *testing.T) {
	dir := t.TempDir()
	sc := config.ServerConfig{
		Name:      "srv",
		Transport: "http",
		URL:       "https://mcp.example.test/mcp",
		Auth:      &config.AuthConfig{Type: config.AuthTypeOAuth2},
	}
	configtest.WriteServer(t, dir, sc)
	failure := fmt.Errorf(
		"invoke: %w",
		&transport.HTTPStatusError{
			StatusCode:      http.StatusForbidden,
			WWWAuthenticate: `Bearer error="insufficient_scope", scope="write"`,
		},
	)
	recordAuthorizationFailure(dir, sc, failure)
	got, err := auth.LoadChallenge(dir, sc.Name, sc.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusForbidden ||
		got.WWWAuthenticate != `Bearer error="insufficient_scope", scope="write"` {
		t.Fatalf("saved challenge = %#v", got)
	}
}

func TestCLIIgnoresNonOAuthFailures(t *testing.T) {
	dir := t.TempDir()
	sc := config.ServerConfig{
		Name:      "srv",
		Transport: "http",
		URL:       "https://mcp.example.test/mcp",
		Auth:      &config.AuthConfig{Type: config.AuthTypeOAuth2},
	}
	configtest.WriteServer(t, dir, sc)
	recordAuthorizationFailure(
		dir,
		sc,
		&transport.HTTPStatusError{StatusCode: http.StatusForbidden, WWWAuthenticate: `Basic realm="private"`},
	)
	if _, err := auth.LoadChallenge(dir, sc.Name, sc.URL); !auth.IsNotFound(err) {
		t.Fatalf("saved Basic challenge: %v", err)
	}
}

func TestCLIStaticTokenDoesNotRecordChallenge(t *testing.T) {
	dir := t.TempDir()
	sc := config.ServerConfig{
		Name: "srv",
		URL:  "https://mcp.example.test",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, Token: "static"},
	}
	recordAuthorizationFailure(
		dir,
		sc,
		&transport.HTTPStatusError{
			StatusCode:      403,
			WWWAuthenticate: `Bearer error="insufficient_scope", scope="write"`,
		},
	)
	if _, err := auth.LoadChallenge(dir, sc.Name, sc.URL); !auth.IsNotFound(err) {
		t.Fatalf("static token challenge persisted: %v", err)
	}
}
