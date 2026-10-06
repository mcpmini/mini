package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
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
	if server, ok := strings.CutSuffix(name, ".proj"); ok {
		return nil, fmt.Errorf(
			"%s: projection files are no longer read; move these rules under projections: in %s.yaml and delete this file",
			path,
			server,
		)
	}
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
	projections, err := decodeProjections(&file.Projections)
	sc.Projections = projections
	if err != nil {
		sc.ProjectionsErr = &SourceError{Err: err}
	}
	return nil
}

func decodeProjections(node *yaml.Node) (map[string]*ProjectionConfig, error) {
	var projections map[string]*ProjectionConfig
	if err := node.Decode(&projections); err != nil {
		return nil, err
	}
	for tool, p := range projections {
		if p == nil {
			continue
		}
		if err := ValidResponseFormat(p.Format); err != nil {
			return nil, fmt.Errorf("projection %s: format: %w", tool, err)
		}
	}
	return projections, nil
}

func (sc ServerConfig) MarshalYAML() (any, error) {
	return struct {
		serverFields `yaml:",inline"`
		Projections  map[string]*ProjectionConfig `yaml:"projections,omitempty"`
	}{serverFields(sc), sc.Projections}, nil
}

func decodeServerFile(path, name string, data []byte) (*ServerConfig, error) {
	var s ServerConfig
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.Name = name
	if s.ProjectionsErr != nil {
		s.ProjectionsErr = &SourceError{
			Path:       path,
			ServerName: name,
			Err:        fmt.Errorf("parse %s: %w", path, s.ProjectionsErr.Err),
		}
	}
	return &s, nil
}

// ReadUnexpandedServer reads a server file as written, before ${VAR} expansion.
func ReadUnexpandedServer(configDir, name string) (ServerConfig, error) {
	path := ServerPath(configDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return ServerConfig{}, err
	}
	sc, err := decodeServerFile(path, name, data)
	if err != nil {
		// yaml errors can quote the offending value, which may be a header token.
		return ServerConfig{}, errors.New(path + " does not parse")
	}
	return *sc, nil
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
	_, err := parseValidServerFile(path, data)
	return err
}

func parseValidServerFile(path string, data []byte) (*ServerConfig, error) {
	sc, err := parseServerConfig(path, data)
	if err != nil {
		return nil, err
	}
	if sc.ProjectionsErr != nil {
		return nil, sc.ProjectionsErr.Err
	}
	return sc, nil
}
