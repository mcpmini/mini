package initcmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestBinaryPath(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(dir, "build", "mini")
	other := filepath.Join(dir, "other", "mini")
	testutil.WriteFile(t, self, "binary")
	testutil.WriteFile(t, other, "another binary")
	link := filepath.Join(dir, "bin", "mini")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, link); err != nil {
		t.Fatal(err)
	}
	executable := func() (string, error) { return self, nil }
	onPath := func(path string) func(string) (string, error) {
		return func(string) (string, error) { return path, nil }
	}
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	for _, tt := range []struct {
		name   string
		lookup binaryLookup
		want   string
	}{
		{"PATH entry linking to this binary", binaryLookup{executable, onPath(link)}, link},
		{"PATH entry that is another binary", binaryLookup{executable, onPath(other)}, self},
		{"not on PATH", binaryLookup{executable, notOnPath}, self},
	} {
		if got := tt.lookup.binaryPath(); got != tt.want {
			t.Errorf("%s: binaryPath = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestMiniCommandArgs(t *testing.T) {
	lookup := binaryLookup{func() (string, error) { return "/opt/mini", nil }, func(string) (string, error) { return "", errors.New("not found") }}
	t.Setenv("HOME", t.TempDir())
	if got := lookup.miniCommand(config.DefaultConfigDir()).Args; !reflect.DeepEqual(got, []string{"connect"}) {
		t.Errorf("args for the default config dir = %v, want [connect]", got)
	}
	other := t.TempDir()
	if got := lookup.miniCommand(other).Args; !reflect.DeepEqual(got, []string{"--config", other, "connect"}) {
		t.Errorf("args for another config dir = %v, want --config %s connect", got, other)
	}
}
