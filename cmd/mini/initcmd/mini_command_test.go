package initcmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
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
	lookup := binaryLookup{
		func() (string, error) { return "/opt/mini", nil },
		func(string) (string, error) { return "", errors.New("not found") },
	}
	t.Setenv("HOME", t.TempDir())
	defaultDir, err := config.DefaultConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := lookup.miniCommand(defaultDir).Args; !reflect.DeepEqual(got, []string{"connect"}) {
		t.Errorf("args for the default config dir = %v, want [connect]", got)
	}
	other := t.TempDir()
	if got := lookup.miniCommand(other).Args; !reflect.DeepEqual(got, []string{"--config", other, "connect"}) {
		t.Errorf("args for another config dir = %v, want --config %s connect", got, other)
	}
}

func TestMiniCommandArgs_UsesExplicitConfigWhenHomeUnavailable(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	dir := t.TempDir()
	lookup := binaryLookup{
		func() (string, error) { return "/opt/mini", nil },
		func(string) (string, error) { return "", errors.New("not found") },
	}
	if got := lookup.miniCommand(dir).Args; !reflect.DeepEqual(got, []string{"--config", dir, "connect"}) {
		t.Fatalf("args = %v, want --config %s connect", got, dir)
	}
}

func TestWithin(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "T")
	link := filepath.Join(dir, "link")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmp, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		filepath.Join(tmp, "go-build1", "exe", "mini"):  true,
		filepath.Join(link, "go-build1", "exe", "mini"): true,
		filepath.Join(dir, "Tools", "mini"):             false,
		filepath.Join(dir, "T-other", "mini"):           false,
	} {
		if got := within(path, []string{tmp}); got != want {
			t.Errorf("within(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestTemporaryDirs(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("GOCACHE", "/cache/go-build")
	dirs := temporaryDirs()
	for _, want := range []string{os.TempDir(), "/cache/go-build", "/home/u/Downloads"} {
		if !slices.Contains(dirs, want) {
			t.Errorf("temporary dirs = %v, want %s among them", dirs, want)
		}
	}
}

func TestSetupTemporaryMini_aBinaryGoTestBuiltLooksTemporary(t *testing.T) {
	t.Setenv("PATH", "")
	if path, temporary := (Setup{ConfigDir: t.TempDir()}).TemporaryMini(); !temporary {
		t.Errorf("TemporaryMini = %s, false; want the test binary, built under the temp dir, to look temporary", path)
	}
}
