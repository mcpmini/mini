package config

import (
	"fmt"
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
	path, err := existingServerPath(p.ConfigDir, p.ServerName)
	if err != nil {
		return nil, err
	}
	edit := projectionEdit{path: path, tool: p.Tool, requested: p.Projection}
	var saved *ProjectionConfig
	_, err = fileio.EditFile(fileio.EditParams{Path: path, Edit: func(data []byte) ([]byte, error) {
		edited, rule, err := edit.apply(data)
		saved = rule
		return edited, err
	}})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

type projectionEdit struct {
	path, tool string
	requested  *ProjectionConfig
}

func (e projectionEdit) apply(data []byte) ([]byte, *ProjectionConfig, error) {
	before, doc, err := parseEditableServerFile(e.path, data)
	if err != nil {
		return nil, nil, err
	}
	want, err := setProjectionNode(doc.Content[0], e.tool, keepAlias(e.requested, before.Projections[e.tool]))
	if err != nil {
		return nil, nil, err
	}
	if reflect.DeepEqual(want, before.Projections[e.tool]) {
		return data, want, nil
	}
	edited, err := encodeServerYAML(doc)
	if err != nil {
		return nil, nil, err
	}
	saved, err := e.checkOnlyTheRuleChanged(edited, before, want)
	return edited, saved, err
}

// checkOnlyTheRuleChanged loads the edited file, since rules can reach projections: through YAML merge
// keys and anchors, which editing one key can't account for.
func (e projectionEdit) checkOnlyTheRuleChanged(
	edited []byte,
	before *ServerConfig,
	want *ProjectionConfig,
) (*ProjectionConfig, error) {
	after, err := parseValidServerFile(e.path, edited)
	if err != nil {
		return nil, fmt.Errorf("the edited server file would not load: %w", err)
	}
	if !reflect.DeepEqual(after.Projections[e.tool], want) {
		return nil, fmt.Errorf(
			"the rule for %s comes through a YAML merge key or anchor, which a save can't change",
			e.tool,
		)
	}
	if !sameRulesExcept(before.Projections, after.Projections, e.tool) {
		return nil, fmt.Errorf(
			"saving %s would also change other tools' rules, which come through a YAML merge key or anchor",
			e.tool,
		)
	}
	return after.Projections[e.tool], nil
}

func parseEditableServerFile(path string, data []byte) (*ServerConfig, *yaml.Node, error) {
	sc, err := parseValidServerFile(path, data)
	if err != nil {
		return nil, nil, fmt.Errorf("the server file does not load: %w", err)
	}
	doc, err := parseServerDocument(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return sc, doc, nil
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
