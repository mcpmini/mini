package response

import (
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func TestStoreConfigFrom(t *testing.T) {
	t.Run("uses ResponseDir when set", func(t *testing.T) {
		cfg := &config.Config{ResponseDir: "/custom/path", ResponseTTL: "2h", ResponseDiskBudgetMB: 100}
		sc := StoreConfigFrom(cfg)
		if sc.Dir != "/custom/path" {
			t.Errorf("Dir = %q, want /custom/path", sc.Dir)
		}
		if sc.TTL != 2*time.Hour {
			t.Errorf("TTL = %v, want 2h", sc.TTL)
		}
		if sc.BudgetMB != 100 {
			t.Errorf("BudgetMB = %d, want 100", sc.BudgetMB)
		}
		if sc.CleanupInterval != time.Hour {
			t.Errorf("CleanupInterval = %v, want 1h", sc.CleanupInterval)
		}
	})

	t.Run("defaults to home dir when ResponseDir empty", func(t *testing.T) {
		cfg := &config.Config{}
		sc := StoreConfigFrom(cfg)
		if !strings.HasSuffix(sc.Dir, "/.mini/internal/responses") {
			t.Errorf("Dir = %q, want suffix /.mini/internal/responses", sc.Dir)
		}
	})

	t.Run("defaults TTL to 1h on empty or invalid", func(t *testing.T) {
		for _, spec := range []string{"", "notaduration"} {
			cfg := &config.Config{ResponseTTL: spec}
			sc := StoreConfigFrom(cfg)
			if sc.TTL != time.Hour {
				t.Errorf("TTL with %q = %v, want 1h", spec, sc.TTL)
			}
		}
	})
}
