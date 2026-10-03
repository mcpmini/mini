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

func WriteAction(t testing.TB, dir string, action config.ActionConfig) {
	t.Helper()
	if !config.ValidServerName.MatchString(action.Name) {
		t.Fatalf("invalid action name %q", action.Name)
	}
	testutil.WriteFileBytes(t, filepath.Join(dir, "internal", "actions", action.Name+".yaml"), writeYAML(t, action))
}
