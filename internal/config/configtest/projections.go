package configtest

import (
	"bytes"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
	"gopkg.in/yaml.v3"
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
	var doc yaml.Node
	if err := yaml.Unmarshal(testutil.ReadFile(t, path), &doc); err != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		t.Fatalf("server fixture %s must hold one mapping: %v", path, err)
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "projections" {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			break
		}
	}
	if projections != nil {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "projections"}, projections)
	}
	testutil.WriteFileBytes(t, path, encodeServerFixture(t, &doc))
}

func encodeServerFixture(t testing.TB, doc *yaml.Node) []byte {
	t.Helper()
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(4)
	if err := encoder.Encode(doc); err != nil {
		t.Fatalf("encode server fixture: %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("encode server fixture: %v", err)
	}
	return out.Bytes()
}
