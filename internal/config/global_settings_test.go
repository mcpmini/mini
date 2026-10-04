package config_test

import (
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
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
