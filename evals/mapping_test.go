//go:build evals

package evals_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MCPMapping builds the fixture directory for a fake MCP server.
// fakemcp's mergeArgs overlays Claude's actual input args onto top-level
// response fields with matching names, so write operations echo back Claude's values.
type MCPMapping struct {
	responses map[string]json.RawMessage
}

func NewMCPMapping() *MCPMapping {
	return &MCPMapping{responses: make(map[string]json.RawMessage)}
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		tool := strings.TrimSuffix(e.Name(), ".json")
		if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			m.responses[tool] = data
		}
	}
	return m
}

// WriteOp registers a tool that auto-generates its response from request args.
// Use for create_*, update_*, post_* and other mutating operations.
func (m *MCPMapping) WriteOp(tool string) *MCPMapping {
	m.responses[tool] = json.RawMessage(`{"__write_op": true}`)
	return m
}

func (m *MCPMapping) Dir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for tool, data := range m.responses {
		if err := os.WriteFile(filepath.Join(d, tool+".json"), data, 0600); err != nil {
			t.Fatalf("MCPMapping.Dir: write %s: %v", tool, err)
		}
	}
	return d
}
