package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
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

const saveAttempts = 3

var errServerFileChanged = errors.New("the server file changed while saving")

type replaceFunc func(path string, data []byte, opts fileio.ReplaceOptions) error

// SaveServerProjection replaces one tool's rule under projections: in the server's file, leaving the
// rest of the file as it was, and returns the rule the file now holds for the tool.
func SaveServerProjection(p ServerProjectionParams) (*ProjectionConfig, error) {
	return saveServerProjection(p, fileio.ReplaceFile)
}

func saveServerProjection(p ServerProjectionParams, replace replaceFunc) (*ProjectionConfig, error) {
	if err := checkServerName(p.ServerName, "the request"); err != nil {
		return nil, err
	}
	for range saveAttempts {
		saved, err := saveProjectionOnce(p, replace)
		if !errors.Is(err, errServerFileChanged) {
			return saved, err
		}
	}
	return nil, fmt.Errorf("%w on each of %d attempts", errServerFileChanged, saveAttempts)
}

func saveProjectionOnce(p ServerProjectionParams, replace replaceFunc) (*ProjectionConfig, error) {
	source, err := readServerSource(p.ConfigDir, p.ServerName)
	if err != nil {
		return nil, err
	}
	edited, saved, err := editProjection(source, p.Tool, p.Projection)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(edited, source.data) {
		return saved, nil
	}
	opts := fileio.ReplaceOptions{Perm: source.mode, BeforeRename: source.checkUnchanged}
	if err := replace(source.target, edited, opts); err != nil {
		return nil, err
	}
	return saved, nil
}

type serverSource struct {
	path   string
	target string
	data   []byte
	mode   os.FileMode
}

func readServerSource(configDir, name string) (serverSource, error) {
	path := ServerPath(configDir, name)
	if !ServerFileExists(configDir, name) {
		return serverSource{}, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return serverSource{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return serverSource{}, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return serverSource{}, err
	}
	return serverSource{path: path, target: target, data: data, mode: info.Mode().Perm()}, nil
}

func (s serverSource) checkUnchanged() error {
	target, err := filepath.EvalSymlinks(s.path)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if target != s.target || !bytes.Equal(current, s.data) {
		return errServerFileChanged
	}
	return nil
}

func editProjection(source serverSource, tool string, requested *ProjectionConfig) ([]byte, *ProjectionConfig, error) {
	before, doc, err := parseEditableServerFile(source.path, source.data)
	if err != nil {
		return nil, nil, err
	}
	want, err := setProjectionNode(doc.Content[0], tool, keepAlias(requested, before.Projections[tool]))
	if err != nil {
		return nil, nil, err
	}
	if reflect.DeepEqual(want, before.Projections[tool]) {
		return source.data, want, nil
	}
	edited, err := yaml.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	saved, err := checkOnlyTheRuleChanged(source.path, edited, before, tool, want)
	return edited, saved, err
}

// checkOnlyTheRuleChanged loads the edited file, since rules can reach projections: through YAML merge
// keys and anchors, which editing one key can't account for.
func checkOnlyTheRuleChanged(path string, edited []byte, before *ServerConfig, tool string, want *ProjectionConfig) (*ProjectionConfig, error) {
	after, err := parseValidServerFile(path, edited)
	if err != nil {
		return nil, fmt.Errorf("the edited server file would not load: %w", err)
	}
	if !reflect.DeepEqual(after.Projections[tool], want) || !sameRulesExcept(before.Projections, after.Projections, tool) {
		return nil, fmt.Errorf("saving %s would also change rules that come through a YAML merge key or anchor", tool)
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
