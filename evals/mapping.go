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
		m.loadFixtureFile(dir, e.Name())
	}
	return m
}

func (m *MCPMapping) loadFixtureFile(dir, name string) {
	tool, target := fixtureTarget(m, name)
	if tool == "" {
		return
	}
	if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
		target[tool] = data
	}
}

func fixtureTarget(m *MCPMapping, name string) (string, map[string]json.RawMessage) {
	switch {
	case strings.HasSuffix(name, ".schema.json"):
		return strings.TrimSuffix(name, ".schema.json"), m.schemas
	case strings.HasSuffix(name, ".json"):
		return strings.TrimSuffix(name, ".json"), m.responses
	default:
		return "", nil
	}
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
