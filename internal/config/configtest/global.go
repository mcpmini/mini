package configtest

import (
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func WriteConfig(t testing.TB, dir string, cfg *config.Config) {
	t.Helper()
	testutil.WriteFileBytes(t, filepath.Join(dir, "config.yaml"), writeYAML(t, cfg))
}
