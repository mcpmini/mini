package configtest

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

type ProjectionFile struct {
	ServerName string
	Tools      map[string]*config.ProjectionConfig
}

// WriteProjections replaces projections: in the server's existing file; no tools removes the block.
func WriteProjections(t testing.TB, dir string, p ProjectionFile) {
	t.Helper()
	var projections *yaml.Node
	if len(p.Tools) > 0 {
		projections = &yaml.Node{}
		if err := projections.Encode(p.Tools); err != nil {
			t.Fatalf("encode projections: %v", err)
		}
	}
	replaceServerProjections(t, dir, p.ServerName, projections)
}

// WriteRawProjections replaces projections: with YAML the typed form can't express, such as a rule of the wrong type.
func WriteRawProjections(t testing.TB, dir, serverName, projections string) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(projections), &doc); err != nil || len(doc.Content) != 1 {
		t.Fatalf("parse raw projections %q: %v", projections, err)
	}
	replaceServerProjections(t, dir, serverName, doc.Content[0])
}

func replaceServerProjections(t testing.TB, dir, serverName string, projections *yaml.Node) {
	t.Helper()
	if !config.ValidServerName.MatchString(serverName) {
		t.Fatalf("invalid server name %q", serverName)
	}
	path := config.ServerPath(dir, serverName)
	data, err := config.ReplaceServerKey(testutil.ReadFile(t, path), "projections", projections)
	if err != nil {
		t.Fatalf("server fixture %s: %v", path, err)
	}
	testutil.WriteFileBytes(t, path, data)
}
