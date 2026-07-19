package config_test

import (
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func TestParseTimeoutSpec(t *testing.T) {
	tests := []struct {
		name        string
		spec        string
		def         time.Duration
		wantD       time.Duration
		wantEnabled bool
		wantErr     bool
	}{
		{name: "empty uses default", spec: "", def: 10 * time.Second, wantD: 10 * time.Second, wantEnabled: true},
		{name: "zero disables", spec: "0", def: 10 * time.Second, wantEnabled: false},
		{name: "explicit duration", spec: "3s", def: 10 * time.Second, wantD: 3 * time.Second, wantEnabled: true},
		{name: "unparseable is rejected", spec: "nonsense", def: 10 * time.Second, wantErr: true},
		{name: "negative is rejected", spec: "-1s", def: 10 * time.Second, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, enabled, err := config.ParseTimeoutSpec(tc.spec, tc.def)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for spec %q", tc.spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if enabled != tc.wantEnabled {
				t.Fatalf("enabled = %v, want %v", enabled, tc.wantEnabled)
			}
			if tc.wantEnabled && d != tc.wantD {
				t.Fatalf("d = %v, want %v", d, tc.wantD)
			}
		})
	}
}
