package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/fileio"
)

func checkServerName(name, source string) error {
	if !ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q in %s: must match ^[a-zA-Z0-9_-]+$", name, source)
	}
	return nil
}

func loadServerConfig(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseServerConfig(path, data)
}

func parseServerConfig(path string, data []byte) (*ServerConfig, error) {
	name := serverNameFromPath(path)
	if err := checkServerName(name, path); err != nil {
		return nil, err
	}
	s, err := decodeServerFile(path, name, data)
	if err != nil {
		return nil, err
	}
	if _, err := ParseTimeoutSpec(s.HandshakeTimeout, 0); err != nil {
		return nil, fmt.Errorf("invalid handshake_timeout in %s: %w", path, err)
	}
	if err := checkUnexpandedFields(*s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	expandServerEnv(s)
	return s, nil
}

type serverFields ServerConfig

// UnmarshalYAML records a mistake in projections in ProjectionsErr instead of returning it, so it
// costs the server only its projections.
func (sc *ServerConfig) UnmarshalYAML(value *yaml.Node) error {
	var file struct {
		serverFields `yaml:",inline"`
		Projections  yaml.Node `yaml:"projections"`
	}
	if err := value.Decode(&file); err != nil {
		return err
	}
	*sc = ServerConfig(file.serverFields)
	if file.Projections.Kind == 0 {
		return nil
	}
	if err := file.Projections.Decode(&sc.Projections); err != nil {
		sc.Projections = nil
		sc.ProjectionsErr = &SourceError{Err: err}
	}
	return nil
}

func (sc ServerConfig) MarshalYAML() (any, error) {
	var projections *map[string]*ProjectionConfig
	if sc.Projections != nil {
		projections = &sc.Projections
	}
	return struct {
		serverFields `yaml:",inline"`
		Projections  *map[string]*ProjectionConfig `yaml:"projections,omitempty"`
	}{serverFields(sc), projections}, nil
}

func decodeServerFile(path, name string, data []byte) (*ServerConfig, error) {
	var s ServerConfig
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.Name = name
	if s.ProjectionsErr != nil {
		s.ProjectionsErr = &SourceError{Path: path, ServerName: name, Err: fmt.Errorf("parse %s: %w", path, s.ProjectionsErr.Err)}
	}
	return &s, nil
}

func ServerPath(configDir, name string) string {
	return filepath.Join(configDir, "servers", name+".yaml")
}

// ServerFileExists matches the name exactly: a case-insensitive disk would otherwise
// treat "GitHub" as github.yaml, and act on that server under the wrong name.
func ServerFileExists(configDir, name string) bool {
	path := ServerPath(configDir, name)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == filepath.Base(path) })
}

func serverNameFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".yaml")
}

// ValidateServerFile checks data as a server file at path, which names the server. An unset ${VAR}
// passes, since it only has to be set where mini runs.
func ValidateServerFile(path string, data []byte) error {
	sc, err := parseServerConfig(path, data)
	if err != nil {
		return err
	}
	if sc.ProjectionsErr != nil {
		return sc.ProjectionsErr.Err
	}
	return validateServerProjectionFormats(sc.Name, sc.Projections)
}

type ServerProjectionParams struct {
	ConfigDir  string
	ServerName string
	Tool       string
	Projection *ProjectionConfig
}

type replaceServerProjectionParams struct {
	request ServerProjectionParams
	replace func(string, []byte, fileio.ReplaceOptions) error
}

func ReplaceServerProjection(p ServerProjectionParams) (*ProjectionConfig, error) {
	return replaceServerProjection(replaceServerProjectionParams{request: p, replace: fileio.ReplaceFile})
}

func replaceServerProjection(p replaceServerProjectionParams) (*ProjectionConfig, error) {
	req := p.request
	if err := validateProjectionRequest(req); err != nil {
		return nil, err
	}
	path := ServerPath(req.ConfigDir, req.ServerName)
	for range 3 {
		projection, changed, err := replaceProjectionAttempt(p)
		if err == nil {
			return projection, nil
		}
		if !changed {
			return nil, fmt.Errorf("replace %s: %w", path, err)
		}
	}
	return nil, fmt.Errorf("replace %s: changed during all three save attempts", path)
}

func validateProjectionRequest(req ServerProjectionParams) error {
	if err := checkServerName(req.ServerName, "the request"); err != nil {
		return err
	}
	if req.Tool != "*" && !ValidToolName.MatchString(req.Tool) {
		return fmt.Errorf("invalid tool name %q", req.Tool)
	}
	return nil
}

func replaceProjectionAttempt(p replaceServerProjectionParams) (*ProjectionConfig, bool, error) {
	req := p.request
	path := ServerPath(req.ConfigDir, req.ServerName)
	source, err := readServerTarget(path, req.ServerName)
	if err != nil {
		return nil, false, err
	}
	updated, committed, err := editServerProjection(path, req, source.data)
	if err != nil {
		return nil, false, err
	}
	checkState := unchangedServerSource{path: path, target: source.target, original: source.data}
	check := func() error { return checkState.check() }
	err = p.replace(source.target, updated, fileio.ReplaceOptions{Perm: source.info.Mode().Perm(), BeforeRename: check})
	if err != nil {
		return nil, checkState.changed, err
	}
	return committed, false, nil
}

type unchangedServerSource struct {
	path, target string
	original     []byte
	changed      bool
}

func (s *unchangedServerSource) check() error {
	path, target, original := s.path, s.target, s.original
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if resolved != target {
		s.changed = true
		return errServerFileChanged
	}
	current, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	if !slices.Equal(current, original) {
		s.changed = true
		return errServerFileChanged
	}
	return nil
}

var errServerFileChanged = errors.New("server file changed during save")

type serverFileSource struct {
	target string
	data   []byte
	info   os.FileInfo
}

func readServerTarget(path, name string) (serverFileSource, error) {
	if !ServerFileExists(filepath.Dir(filepath.Dir(path)), name) {
		return serverFileSource{}, fmt.Errorf("read %s: %w", path, os.ErrNotExist)
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return serverFileSource{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return serverFileSource{}, fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return serverFileSource{}, fmt.Errorf("stat %s: %w", path, err)
	}
	return serverFileSource{target: target, data: data, info: info}, nil
}

type projectionEdit struct {
	doc         yaml.Node
	root        *yaml.Node
	projections *yaml.Node
	before      *ServerConfig
}

func editServerProjection(path string, request ServerProjectionParams, data []byte) ([]byte, *ProjectionConfig, error) {
	edit, err := readProjectionEdit(path, data)
	if err != nil {
		return nil, nil, err
	}
	desired := desiredProjection(edit, request)
	if reflect.DeepEqual(edit.before.Projections[request.Tool], desired) {
		return data, desired, nil
	}
	if err := applyProjectionEdit(edit, request.Tool, desired); err != nil {
		return nil, nil, err
	}
	return edit.encode(projectionEncodeParams{path: path, tool: request.Tool, desired: desired, original: data})
}

func readProjectionEdit(path string, data []byte) (*projectionEdit, error) {
	if err := ValidateServerFile(path, data); err != nil {
		return nil, fmt.Errorf("server config cannot be safely edited: %w; fix the file or pass session_only", err)
	}
	doc, err := decodeSingleServerDocument(data)
	if err != nil {
		return nil, err
	}
	before, err := parseServerConfig(path, data)
	if err != nil {
		return nil, err
	}
	edit := &projectionEdit{doc: doc, root: doc.Content[0], before: before}
	edit.projections = mappingValue(edit.root, "projections")
	if err := validateProjectionNode(edit); err != nil {
		return nil, err
	}
	return edit, nil
}

func desiredProjection(edit *projectionEdit, request ServerProjectionParams) *ProjectionConfig {
	projection := cloneProjection(request.Projection)
	existing := edit.before.Projections[request.Tool]
	if projection != nil && projection.Alias == "" && existing != nil {
		projection.Alias = existing.Alias
		return projection
	}
	if projection == nil && existing != nil && existing.Alias != "" {
		return &ProjectionConfig{Alias: existing.Alias}
	}
	return projection
}

func applyProjectionEdit(edit *projectionEdit, tool string, desired *ProjectionConfig) error {
	if edit.projections == nil || edit.projections.Tag == "!!null" {
		edit.projections = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(edit.root, "projections", edit.projections)
	}
	if desired == nil {
		removeMappingValue(edit.projections, tool)
	} else {
		var node yaml.Node
		if err := node.Encode(desired); err != nil {
			return err
		}
		setMappingValue(edit.projections, tool, &node)
	}
	if len(edit.projections.Content) == 0 && desired == nil {
		removeMappingValue(edit.root, "projections")
	}
	return nil
}

type projectionEncodeParams struct {
	path, tool string
	desired    *ProjectionConfig
	original   []byte
}

func (edit *projectionEdit) encode(p projectionEncodeParams) ([]byte, *ProjectionConfig, error) {
	path, tool, desired, original := p.path, p.tool, p.desired, p.original
	updated, err := yaml.Marshal(&edit.doc)
	if err != nil {
		return nil, nil, err
	}
	if bytes.Equal(updated, original) {
		return original, edit.before.Projections[tool], nil
	}
	if err := ValidateServerFile(path, updated); err != nil {
		return nil, nil, fmt.Errorf("edited server config is invalid: %w; pass session_only", err)
	}
	after, err := parseServerConfig(path, updated)
	if err != nil {
		return nil, nil, err
	}
	if err := edit.preserveEffectiveRules(tool, desired, after.Projections); err != nil {
		return nil, nil, err
	}
	return updated, after.Projections[tool], nil
}

func (edit *projectionEdit) preserveEffectiveRules(target string, desired *ProjectionConfig, after map[string]*ProjectionConfig) error {
	before := edit.before.Projections
	if !reflect.DeepEqual(after[target], desired) {
		return fmt.Errorf("editing %s would change its effective projection; pass session_only", target)
	}
	for tool, rule := range before {
		if tool != target && !reflect.DeepEqual(rule, after[tool]) {
			return fmt.Errorf("editing %s would change projection %s; pass session_only", target, tool)
		}
	}
	for tool, rule := range after {
		if tool != target && !reflect.DeepEqual(rule, before[tool]) {
			return fmt.Errorf("editing %s would change projection %s; pass session_only", target, tool)
		}
	}
	return nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
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

func cloneProjection(projection *ProjectionConfig) *ProjectionConfig {
	if projection == nil {
		return nil
	}
	copy := *projection
	return &copy
}
