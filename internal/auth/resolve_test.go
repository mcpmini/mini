//go:build test

package auth_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func TestValidateOAuthServer(t *testing.T) {
	tests := []struct {
		name    string
		sc      config.ServerConfig
		wantErr bool
	}{
		{"no auth", config.ServerConfig{Name: "s"}, true},
		{"apikey auth", config.ServerConfig{Name: "s", Auth: &config.AuthConfig{Type: "apikey"}}, true},
		{"oauth2 auth", config.ServerConfig{Name: "s", Auth: &config.AuthConfig{Type: "oauth2"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.ValidateOAuthServer("s", tc.sc)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateOAuthServer() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
