package config_test

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestDefaultConfig_hasExpectedValues(t *testing.T) {
	cfg := config.DefaultConfig()
	assertDefaultConfigFields(t, cfg)
}

func assertDefaultConfigFields(t *testing.T, cfg *config.Config) {
	t.Helper()
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"DefaultDepthLimit", cfg.DefaultDepthLimit, 0},
		{"DefaultStringLimit", cfg.DefaultStringLimit, 2000},
		{"LogLevel", cfg.LogLevel, "info"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, c.got)
		}
	}
	if len(cfg.ContentFields) == 0 {
		t.Error("expected non-empty default content fields")
	}
}

func TestServerConfig_IsEnabled(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name string
		sc   config.ServerConfig
		want bool
	}{
		{"nil enabled field defaults to true", config.ServerConfig{}, true},
		{"explicit true", config.ServerConfig{Enabled: &enabled}, true},
		{"explicit false", config.ServerConfig{Enabled: &disabled}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.IsEnabled(); got != tc.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}
