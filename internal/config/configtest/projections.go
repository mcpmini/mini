package configtest

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

type ProjectionFile struct {
	ServerName string
	Tools      map[string]*config.ProjectionConfig
}

func WriteProjections(t testing.TB, dir string, p ProjectionFile) {
	t.Helper()
	if !config.ValidServerName.MatchString(p.ServerName) {
		t.Fatalf("invalid server name %q", p.ServerName)
	}
	WriteProjectionFile(t, config.ProjectionPath(dir, p.ServerName), p.Tools)
}

func WriteProjectionFile(t testing.TB, path string, tools map[string]*config.ProjectionConfig) {
	t.Helper()
	testutil.WriteFileBytes(t, path, writeYAML(t, tools))
}
