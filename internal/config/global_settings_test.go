package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestLoadLiteralDefaultStringLimit(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), "default_string_limit: 50\n")
	got, err := config.LoadMain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultStringLimit != 50 {
		t.Fatalf("default_string_limit = %d, want 50 from user YAML", got.DefaultStringLimit)
	}
}

func TestLoadContentFieldsDistinguishesEmptyFromOmitted(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       int
	}{
		{"explicit empty", "content_fields: []\n", 0},
		{"omitted", "{}\n", len(config.DefaultContentFields)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), tc.yaml)
			got, err := config.LoadMain(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.ContentFields) != tc.want {
				t.Fatalf("content_fields = %v, want %d fields", got.ContentFields, tc.want)
			}
		})
	}
}

func TestLoadMainRefusesAConfigItCannotLoadInFull(t *testing.T) {
	valid := config.DefaultConfig()
	valid.DisableAuthBrowserOpen = true
	badFormat := config.DefaultConfig()
	badFormat.ResponseFormat = "bogus"
	tests := []struct {
		name    string
		cfg     *config.Config
		rawYAML string
		wantErr string
	}{
		{"valid settings load", valid, "", ""},
		{"invalid YAML", nil, "bad: [yaml\n", "parse config"},
		{"invalid response_format", badFormat, "", "response_format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.cfg != nil {
				configtest.WriteConfig(t, dir, tt.cfg)
			} else {
				testutil.WriteFile(t, filepath.Join(dir, "config.yaml"), tt.rawYAML)
			}
			cfg, err := config.LoadMain(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || cfg != nil {
					t.Errorf("LoadMain = (%#v, %v), want (nil, error containing %q) rather than defaults", cfg, err, tt.wantErr)
				}
				return
			}
			if err != nil || cfg == nil || !cfg.DisableAuthBrowserOpen {
				t.Errorf("LoadMain = (%#v, %v), want disable_auth_browser_open", cfg, err)
			}
		})
	}
}
