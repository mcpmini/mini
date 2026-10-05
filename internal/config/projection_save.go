package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"reflect"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/fileio"
)

type ServerProjectionParams struct {
	ConfigDir  string
	ServerName string
	Tool       string
	// Projection nil deletes the tool's rule.
	Projection *ProjectionConfig
}

// SaveServerProjection replaces one tool's rule under projections: in the server's file and returns the
// rule the file now holds for it. Other values and comments stay; the layout is re-encoded.
func SaveServerProjection(p ServerProjectionParams) (*ProjectionConfig, error) {
	if err := checkServerName(p.ServerName, "the request"); err != nil {
		return nil, err
	}
	path := ServerPath(p.ConfigDir, p.ServerName)
	if !ServerFileExists(p.ConfigDir, p.ServerName) {
		return nil, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	var saved *ProjectionConfig
	_, err := fileio.EditFile(fileio.EditParams{Path: path, Edit: func(data []byte) ([]byte, error) {
		edited, rule, err := editProjection(path, data, p.Tool, p.Projection)
		saved = rule
		return edited, err
	}})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func editProjection(path string, data []byte, tool string, requested *ProjectionConfig) ([]byte, *ProjectionConfig, error) {
	before, doc, err := parseEditableServerFile(path, data)
	if err != nil {
		return nil, nil, err
	}
	want, err := setProjectionNode(doc.Content[0], tool, keepAlias(requested, before.Projections[tool]))
	if err != nil {
		return nil, nil, err
	}
	if reflect.DeepEqual(want, before.Projections[tool]) {
		return data, want, nil
	}
	edited, err := yaml.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	saved, err := checkOnlyTheRuleChanged(path, edited, before, tool, want)
	return edited, saved, err
}

// checkOnlyTheRuleChanged loads the edited file, since rules can reach projections: through YAML merge
// keys and anchors, which editing one key can't account for.
func checkOnlyTheRuleChanged(path string, edited []byte, before *ServerConfig, tool string, want *ProjectionConfig) (*ProjectionConfig, error) {
	after, err := parseValidServerFile(path, edited)
	if err != nil {
		return nil, fmt.Errorf("the edited server file would not load: %w", err)
	}
	if !reflect.DeepEqual(after.Projections[tool], want) {
		return nil, fmt.Errorf("the rule for %s comes through a YAML merge key or anchor, which a save can't change", tool)
	}
	if !sameRulesExcept(before.Projections, after.Projections, tool) {
		return nil, fmt.Errorf("saving %s would also change other tools' rules, which come through a YAML merge key or anchor", tool)
	}
	return after.Projections[tool], nil
}

func parseEditableServerFile(path string, data []byte) (*ServerConfig, *yaml.Node, error) {
	sc, err := parseValidServerFile(path, data)
	if err != nil {
		return nil, nil, fmt.Errorf("the server file does not load: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc, extra yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, nil, err
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, nil, fmt.Errorf("%s holds more than one YAML document", path)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s is not a YAML mapping", path)
	}
	return sc, &doc, nil
}

// keepAlias carries the saved alias onto the new rule: the alias is the user's, written in the file,
// and set_projection doesn't set it.
func keepAlias(requested, saved *ProjectionConfig) *ProjectionConfig {
	if saved == nil || saved.Alias == "" {
		return requested
	}
	if requested == nil {
		return &ProjectionConfig{Alias: saved.Alias}
	}
	rule := *requested
	if rule.Alias == "" {
		rule.Alias = saved.Alias
	}
	return &rule
}

// setProjectionNode returns the rule as the file will hold it: written YAML leaves out empty lists and maps.
func setProjectionNode(root *yaml.Node, tool string, rule *ProjectionConfig) (*ProjectionConfig, error) {
	projections := mappingValue(root, "projections")
	if projections == nil || projections.Kind != yaml.MappingNode {
		projections = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(root, "projections", projections)
	}
	if rule == nil {
		removeMappingValue(projections, tool)
		if len(projections.Content) == 0 {
			removeMappingValue(root, "projections")
		}
		return nil, nil
	}
	var node yaml.Node
	if err := node.Encode(rule); err != nil {
		return nil, err
	}
	setMappingValue(projections, tool, &node)
	var written *ProjectionConfig
	if err := node.Decode(&written); err != nil {
		return nil, err
	}
	return written, nil
}

func sameRulesExcept(before, after map[string]*ProjectionConfig, tool string) bool {
	before, after = maps.Clone(before), maps.Clone(after)
	delete(before, tool)
	delete(after, tool)
	return maps.EqualFunc(before, after, func(a, b *ProjectionConfig) bool { return reflect.DeepEqual(a, b) })
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMappingValue(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func removeMappingValue(node *yaml.Node, key string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}
