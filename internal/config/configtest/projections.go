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
	replaceServerProjections(
		replaceServerProjectionsParams{T: t, Dir: dir, ServerName: p.ServerName, Projections: projections},
	)
}

// WriteRawProjections replaces projections: with YAML the typed form can't express, such as a rule of the wrong type.
func WriteRawProjections(params WriteRawProjectionsParams) {
	params.T.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(params.Projections), &doc); err != nil || len(doc.Content) != 1 {
		params.T.Fatalf("parse raw projections %q: %v", params.Projections, err)
	}
	replaceServerProjections(
		replaceServerProjectionsParams{
			T:           params.T,
			Dir:         params.Dir,
			ServerName:  params.ServerName,
			Projections: doc.Content[0],
		},
	)
}

func replaceServerProjections(params replaceServerProjectionsParams) {
	params.T.Helper()
	if !config.ValidServerName.MatchString(params.ServerName) {
		params.T.Fatalf("invalid server name %q", params.ServerName)
	}
	path := config.ServerPath(params.Dir, params.ServerName)
	data, err := config.ReplaceServerKey(testutil.ReadFile(params.T, path), "projections", params.Projections)
	if err != nil {
		params.T.Fatalf("server fixture %s: %v", path, err)
	}
	testutil.WriteFileBytes(params.T, path, data)
}

type WriteRawProjectionsParams struct {
	T           testing.TB
	Dir         string
	ServerName  string
	Projections string
}

type replaceServerProjectionsParams struct {
	T           testing.TB
	Dir         string
	ServerName  string
	Projections *yaml.Node
}
