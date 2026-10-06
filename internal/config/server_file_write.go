package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/fileio"
)

func EncodeServerFile(sc ServerConfig) ([]byte, error) {
	return encodeServerYAML(sc)
}

// CreateServerFile refuses to replace an existing file (fs.ErrExist) and to write one that wouldn't load.
func CreateServerFile(configDir string, sc ServerConfig) (string, error) {
	if err := checkServerName(sc.Name, "the request"); err != nil {
		return "", err
	}
	path := ServerPath(configDir, sc.Name)
	data, err := EncodeServerFile(sc)
	if err != nil {
		return "", err
	}
	if err := ValidateServerFile(path, data); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := fileio.CreateFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// ReplaceServerKey sets a top-level key of a server file, or removes it when value is nil. The result
// isn't checked to load: test fixtures use it to write broken files on purpose.
func ReplaceServerKey(data []byte, key string, value *yaml.Node) ([]byte, error) {
	doc, err := parseServerDocument(data)
	if err != nil {
		return nil, err
	}
	if value == nil {
		removeMappingValue(doc.Content[0], key)
	} else {
		setMappingValue(doc.Content[0], key, value)
	}
	return encodeServerYAML(doc)
}

func parseServerDocument(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc, extra yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("the server file holds more than one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("the server file is not a YAML mapping")
	}
	return &doc, nil
}

func encodeServerYAML(v any) ([]byte, error) {
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(4)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
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
