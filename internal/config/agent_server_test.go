package config_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestValidateAgentServer(t *testing.T) {
	cases := []struct {
		name    string
		sc      config.ServerConfig
		wantErr string
	}{
		{"plain http server", config.ServerConfig{Name: "svc", Transport: "http", URL: "https://example.com/mcp"}, ""},
		{"env reference in url", config.ServerConfig{Name: "svc", URL: "https://example.com/?t=${GITHUB_TOKEN}"}, "environment variable"},
		{"env reference in args", config.ServerConfig{Name: "svc", Command: "run", Args: []string{"${AWS_SECRET}"}}, "environment variable"},
		{"handshake_timeout config load rejects", config.ServerConfig{Name: "svc", URL: "https://example.com", HandshakeTimeout: "soon"}, "handshake_timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateAgentServer(tc.sc)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
