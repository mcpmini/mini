//go:build evals

package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MCPMapping builds the fixture directory for a fake MCP server.
// fakemcp overlays Claude's actual input args onto top-level response fields
// with matching names, so write operations echo back Claude's values.
type MCPMapping struct {
	responses map[string]json.RawMessage
	schemas   map[string]json.RawMessage
}

func NewMCPMapping() *MCPMapping {
	return &MCPMapping{
		responses: make(map[string]json.RawMessage),
		schemas:   make(map[string]json.RawMessage),
	}
}

func (m *MCPMapping) Handle(tool string, response any) *MCPMapping {
	data, _ := json.MarshalIndent(response, "", "  ")
	m.responses[tool] = data
	return m
}

func (m *MCPMapping) HandleRaw(tool string, data json.RawMessage) *MCPMapping {
	m.responses[tool] = data
	return m
}

func (m *MCPMapping) FromFixtureDir(dir string) *MCPMapping {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(dir, name)
		switch {
		case strings.HasSuffix(name, ".schema.json"):
			tool := strings.TrimSuffix(name, ".schema.json")
			if data, err := os.ReadFile(path); err == nil {
				m.schemas[tool] = data
			}
		case strings.HasSuffix(name, ".json"):
			tool := strings.TrimSuffix(name, ".json")
			if data, err := os.ReadFile(path); err == nil {
				m.responses[tool] = data
			}
		}
	}
	return m
}

// WriteOp registers a tool that auto-generates its response from request args.
func (m *MCPMapping) WriteOp(tool string) *MCPMapping {
	m.responses[tool] = json.RawMessage(`{"__write_op": true}`)
	return m
}

func (m *MCPMapping) Dir(env *Env) (string, error) {
	d := env.TempDir()
	for tool, data := range m.responses {
		if err := os.WriteFile(filepath.Join(d, tool+".json"), data, 0600); err != nil {
			return "", fmt.Errorf("write %s: %w", tool, err)
		}
	}
	for tool, schema := range m.schemas {
		if err := os.WriteFile(filepath.Join(d, tool+".schema.json"), schema, 0600); err != nil {
			return "", fmt.Errorf("write schema %s: %w", tool, err)
		}
	}
	return d, nil
}
