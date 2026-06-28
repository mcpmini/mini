//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

type Tool struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	Annotations  json.RawMessage
	Title        json.RawMessage
	OutputSchema json.RawMessage
	Meta         json.RawMessage
	Icons        json.RawMessage
	Execution    json.RawMessage
	FixturePath  string
	Content      string
	WriteOp      bool // synthetic response generated from request args
}

type ToolRegistry struct {
	mu      sync.RWMutex
	tools   map[string]Tool
	callLog string // append JSON-line call records here if non-empty
}

func newToolRegistry(callLog string) *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool), callLog: callLog}
}

func (r *ToolRegistry) LoadFixtures(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !isFixtureEntry(e) {
			continue
		}
		r.Add(buildFixtureTool(dir, e.Name()))
	}
}

func isFixtureEntry(e os.DirEntry) bool {
	return !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasSuffix(e.Name(), ".schema.json")
}

func buildFixtureTool(dir, filename string) Tool {
	name := strings.TrimSuffix(filename, ".json")
	path := filepath.Join(dir, filename)
	schema, annotations := loadSchema(filepath.Join(dir, name+".schema.json"))
	if isWriteOpFile(path) {
		return Tool{Name: name, Description: schemaDescription(schema, name), InputSchema: schema, Annotations: annotations, WriteOp: true}
	}
	return Tool{Name: name, Description: schemaDescription(schema, name+" (fixture)"), InputSchema: schema, Annotations: annotations, FixturePath: path}
}

func loadSchema(path string) (inputSchema, annotations json.RawMessage) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var s struct {
		InputSchema json.RawMessage `json:"inputSchema"`
		Annotations json.RawMessage `json:"annotations"`
	}
	if json.Unmarshal(data, &s) != nil {
		return nil, nil
	}
	if len(s.InputSchema) == 0 {
		s.InputSchema = nil
	}
	if len(s.Annotations) == 0 {
		s.Annotations = nil
	}
	return s.InputSchema, s.Annotations
}

// schemaDescription extracts the description from a schema file, or returns the fallback.
func schemaDescription(schema json.RawMessage, fallback string) string {
	if schema == nil {
		return fallback
	}
	var s struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(schema, &s) == nil && s.Description != "" {
		return s.Description
	}
	return fallback
}

func isWriteOpFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(`"__write_op"`))
}

func (r *ToolRegistry) Add(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name] = t
}

func (r *ToolRegistry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

func (r *ToolRegistry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

func (r *ToolRegistry) MCPTools() []transport.MCPTool {
	tools := r.List()
	out := make([]transport.MCPTool, len(tools))
	for i, t := range tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = emptySchema
		}
		out[i] = transport.MCPTool{
			Name:         t.Name,
			Description:  t.Description,
			InputSchema:  schema,
			Annotations:  t.Annotations,
			Title:        t.Title,
			OutputSchema: t.OutputSchema,
			Meta:         t.Meta,
			Icons:        t.Icons,
			Execution:    t.Execution,
		}
	}
	return out
}

func (r *ToolRegistry) appendCall(tool string) {
	if r.callLog == "" {
		return
	}
	line := fmt.Sprintf("{\"tool\":%q,\"ts\":%q}\n", tool, time.Now().UTC().Format(time.RFC3339Nano))
	f, err := os.OpenFile(r.callLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func (r *ToolRegistry) Call(name string, args map[string]any, sizeBytes int) transport.ToolCallResult {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return errResult("unknown tool: " + name)
	}
	r.appendCall(name)
	if t.WriteOp {
		return syntheticWriteResult(name, args)
	}
	if t.FixturePath != "" {
		return r.callFixture(t.FixturePath, args, sizeBytes)
	}
	return transport.ToolCallResult{Content: []transport.ContentItem{{Type: "text", Text: t.Content}}}
}

func (r *ToolRegistry) callFixture(path string, args map[string]any, sizeBytes int) transport.ToolCallResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return errResult("read fixture: " + err.Error())
	}
	if mcpErr := probeFixtureError(data); mcpErr != "" {
		return errResult(mcpErr)
	}
	data = mergeArgs(data, args)
	content := padContent(string(data), sizeBytes)
	return transport.ToolCallResult{Content: []transport.ContentItem{{Type: "text", Text: content}}}
}

func probeFixtureError(data []byte) string {
	var probe struct {
		MCPError string `json:"__mcp_error"`
	}
	if json.Unmarshal(data, &probe) == nil {
		return probe.MCPError
	}
	return ""
}

func padContent(content string, sizeBytes int) string {
	if sizeBytes > 0 && len(content) < sizeBytes {
		return content + strings.Repeat("x", sizeBytes-len(content))
	}
	return content
}

func syntheticWriteResult(toolName string, args map[string]any) transport.ToolCallResult {
	template, ok := writeOpMappings[toolName]
	if !ok {
		template = map[string]any{"ok": true, "created_at": "$now"}
	}
	data, _ := json.Marshal(applyMapping(template, args))
	return transport.ToolCallResult{Content: []transport.ContentItem{{Type: "text", Text: string(data)}}}
}

// mergeArgs overlays caller-supplied arguments onto top-level keys of a JSON
// object fixture, so Claude sees its own inputs reflected back (e.g. the PR
// title it chose). Keys not present in the fixture are ignored.
func mergeArgs(fixture []byte, args map[string]any) []byte {
	if len(args) == 0 {
		return fixture
	}
	var obj map[string]any
	if json.Unmarshal(fixture, &obj) != nil {
		return fixture // not a JSON object
	}
	mergeExistingArgs(obj, args)
	merged, err := json.Marshal(obj)
	if err != nil {
		return fixture
	}
	return merged
}

func mergeExistingArgs(dst, args map[string]any) {
	for k, v := range args {
		if _, exists := dst[k]; exists {
			dst[k] = v
		}
	}
}

func errResult(msg string) transport.ToolCallResult {
	return transport.ToolCallResult{IsError: true, Content: []transport.ContentItem{{Type: "text", Text: msg}}}
}
