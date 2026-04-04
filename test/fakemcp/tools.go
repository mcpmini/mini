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
	Name        string
	Description string
	FixturePath string
	Content     string
	WriteOp     bool // synthetic response generated from request args
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		path := filepath.Join(dir, e.Name())
		if isWriteOpFile(path) {
			r.Add(Tool{Name: name, Description: name, WriteOp: true})
		} else {
			r.Add(Tool{Name: name, Description: name + " (fixture)", FixturePath: path})
		}
	}
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

func (r *ToolRegistry) MCPTools() []transport.MCPTool {
	tools := r.List()
	out := make([]transport.MCPTool, len(tools))
	for i, t := range tools {
		out[i] = transport.MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
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
	var probe struct {
		MCPError string `json:"__mcp_error"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.MCPError != "" {
		return errResult(probe.MCPError)
	}
	data = mergeArgs(data, args)
	content := string(data)
	if sizeBytes > 0 && len(content) < sizeBytes {
		content = content + strings.Repeat("x", sizeBytes-len(content))
	}
	return transport.ToolCallResult{Content: []transport.ContentItem{{Type: "text", Text: content}}}
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
	for k, v := range args {
		if _, exists := obj[k]; exists {
			obj[k] = v
		}
	}
	merged, err := json.Marshal(obj)
	if err != nil {
		return fixture
	}
	return merged
}

func errResult(msg string) transport.ToolCallResult {
	return transport.ToolCallResult{IsError: true, Content: []transport.ContentItem{{Type: "text", Text: msg}}}
}
