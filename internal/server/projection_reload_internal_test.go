//go:build test

package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func mustFingerprint(t *testing.T, dir string) map[string]string {
	t.Helper()
	fp, err := fingerprintConfigSources(dir)
	if err != nil {
		t.Fatalf("fingerprintConfigSources: %v", err)
	}
	return fp
}

func TestFingerprintProjectionSources(t *testing.T) {
	t.Run("missing servers dir yields empty fingerprint", func(t *testing.T) {
		fp := mustFingerprint(t, t.TempDir())
		if len(fp) != 0 {
			t.Errorf("expected empty fingerprint, got %v", fp)
		}
	})

	t.Run("covers server yaml and proj yaml but not other files", func(t *testing.T) {
		dir := t.TempDir()
		configtest.WriteServer(t, dir, config.ServerConfig{Name: "svc", Transport: "stdio"})
		configtest.WriteProjections(t, dir, configtest.ProjectionFile{
			ServerName: "svc",
			Tools: map[string]*config.ProjectionConfig{
				"tool": {
					IncludeOnly: []string{"a"},
				},
			},
		})
		testutil.WriteFile(t, filepath.Join(dir, "servers", "notes.txt"), "ignored")
		fp := mustFingerprint(t, dir)
		if len(fp) != 2 {
			t.Errorf("expected 2 entries, got %v", fp)
		}
	})

	t.Run("same size content change changes hash", func(t *testing.T) {
		dir := t.TempDir()
		original, replacement := "tool:\n  include_only: [a]\n", "tool:\n  include_only: [b]\n"
		if len(original) != len(replacement) {
			t.Fatal("same-size fixtures differ in size")
		}
		p := config.ProjectionPath(dir, "svc")
		testutil.WriteFile(t, p, original)
		before := mustFingerprint(t, dir)
		testutil.WriteFile(t, p, replacement)
		after := mustFingerprint(t, dir)
		if before[p] == after[p] {
			t.Error("expected hash to change on same-size content edit")
		}
	})

	t.Run("identical content yields identical fingerprint", func(t *testing.T) {
		dir := t.TempDir()
		configtest.WriteServer(t, dir, config.ServerConfig{Name: "svc", Transport: "stdio"})
		if a, b := mustFingerprint(t, dir), mustFingerprint(t, dir); !reflect.DeepEqual(a, b) {
			t.Errorf("expected stable fingerprint, got %v vs %v", a, b)
		}
	})

	t.Run("unreadable file keeps siblings and reads as changed once it recovers", func(t *testing.T) {
		dir := t.TempDir()
		configtest.WriteServer(t, dir, config.ServerConfig{Name: "other", Transport: "stdio"})
		sibling := filepath.Join(dir, "servers", "other.yaml")
		broken := filepath.Join(dir, "servers", "svc.yaml")
		if err := os.Mkdir(broken, 0700); err != nil {
			t.Fatal(err)
		}
		first, second := mustFingerprint(t, dir), mustFingerprint(t, dir)
		if _, ok := first[sibling]; !ok {
			t.Fatalf("fingerprint %v is missing the readable sibling", first)
		}
		if first[broken] == "" || first[broken] != second[broken] {
			t.Fatalf("unreadable file fingerprints %q then %q, want one stable non-empty value", first[broken], second[broken])
		}
		if err := os.Remove(broken); err != nil {
			t.Fatal(err)
		}
		configtest.WriteServer(t, dir, config.ServerConfig{Name: "svc", Transport: "stdio"})
		if recovered := mustFingerprint(t, dir); recovered[broken] == first[broken] {
			t.Error("fingerprint unchanged after the unreadable file became readable")
		}
	})

	t.Run("file absent at hash time is skipped", func(t *testing.T) {
		if h, ok := fileFingerprint(filepath.Join(t.TempDir(), "gone.yaml")); ok {
			t.Errorf("fileFingerprint(absent) = %q, true; want it skipped", h)
		}
	})

	t.Run("ignores config.yaml", func(t *testing.T) {
		dir := t.TempDir()
		fixtureConfig := config.DefaultConfig()
		fixtureConfig.LogLevel = "debug"
		configtest.WriteConfig(t, dir, fixtureConfig)

		if fp := mustFingerprint(t, dir); len(fp) != 0 {
			t.Errorf("fingerprint = %v, want config.yaml left out", fp)
		}
	})
}

func TestChangedPaths(t *testing.T) {
	tests := []struct {
		name string
		prev map[string]string
		curr map[string]string
		want []string
	}{
		{name: "no change", prev: map[string]string{"a": "1"}, curr: map[string]string{"a": "1"}, want: nil},
		{name: "nil prev reports all current", prev: nil, curr: map[string]string{"a": "1", "b": "2"}, want: []string{"a", "b"}},
		{name: "added file", prev: map[string]string{"a": "1"}, curr: map[string]string{"a": "1", "b": "2"}, want: []string{"b"}},
		{name: "removed file", prev: map[string]string{"a": "1", "b": "2"}, curr: map[string]string{"a": "1"}, want: []string{"b"}},
		{name: "modified hash", prev: map[string]string{"a": "1"}, curr: map[string]string{"a": "9"}, want: []string{"a"}},
		{name: "rename is add plus remove", prev: map[string]string{"a": "1"}, curr: map[string]string{"b": "1"}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changedPaths(tt.prev, tt.curr); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("changedPaths(%v, %v) = %v, want %v", tt.prev, tt.curr, got, tt.want)
			}
		})
	}
}
