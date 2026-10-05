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

func WriteProjections(t testing.TB, dir string, p ProjectionFile) {
	t.Helper()
	if !config.ValidServerName.MatchString(p.ServerName) {
		t.Fatalf("invalid server name %q", p.ServerName)
	}
	path := config.ServerPath(dir, p.ServerName)
	doc := readServerFixture(t, path)
	setProjectionFixture(t, doc.Content[0], p.Tools)
	writeServerFixture(t, path, doc)
}

func readServerFixture(t testing.TB, path string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(testutil.ReadFile(t, path), &doc); err != nil {
		t.Fatalf("parse server fixture: %v", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		t.Fatalf("server fixture %s must contain one mapping document", path)
	}
	return &doc
}

func setProjectionFixture(t testing.TB, root *yaml.Node, tools map[string]*config.ProjectionConfig) {
	t.Helper()
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "projections" {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			break
		}
	}
	if len(tools) == 0 {
		return
	}
	var projections yaml.Node
	if err := projections.Encode(tools); err != nil {
		t.Fatalf("encode projection fixture: %v", err)
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "projections"}, &projections)
}

func writeServerFixture(t testing.TB, path string, doc *yaml.Node) {
	t.Helper()
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(4)
	if err := encoder.Encode(doc); err != nil {
		t.Fatalf("encode server fixture: %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("close server fixture: %v", err)
	}
	testutil.WriteFileBytes(t, path, output.Bytes())
}
