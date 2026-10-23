package transport

import (
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
)

type headerParameter struct {
	name string
	path []string
}

func (c *HTTPConnection) rebuildToolHeaders(tools []ToolDefinition) []ToolDefinition {
	entries := make(map[string][]headerParameter, len(tools))
	valid := make([]ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		params, reason := parseHeaderParameters(tool.InputSchema)
		if reason != "" {
			slog.Warn("dropping tool with invalid x-mcp-header",
				"server", c.serverName, "tool", tool.Name, "reason", reason)
			continue
		}
		entries[tool.Name] = params
		valid = append(valid, tool)
	}
	c.modernMu.Lock()
	c.modernHeaders = entries
	c.modernMu.Unlock()
	return valid
}

func (c *HTTPConnection) ensureToolHeaders(ctx context.Context) error {
	c.modernListMu.Lock()
	defer c.modernListMu.Unlock()
	c.modernMu.RLock()
	listed := c.modernHeaders != nil
	c.modernMu.RUnlock()
	if listed {
		return nil
	}
	_, err := c.ListTools(ctx)
	return err
}

func (c *HTTPConnection) negotiatedModernVersion() string {
	if v := c.modernVersion.Load(); v != nil {
		return *v
	}
	return ""
}

func (c *HTTPConnection) isModern() bool {
	return c.negotiatedModernVersion() != ""
}

func (c *HTTPConnection) resetModernEra() {
	c.initMu.Lock()
	c.eraKnown = false
	c.modernVersion.Store(nil)
	c.initMu.Unlock()
}

func encodeModernHeaderValue(value string) string {
	plain := !strings.HasPrefix(value, "=?base64?") || !strings.HasSuffix(value, "?=")
	plain = plain && strings.Trim(value, " \t") == value
	for i := 0; plain && i < len(value); i++ {
		plain = (value[i] >= 0x20 && value[i] <= 0x7e) || value[i] == '\t'
	}
	if plain {
		return value
	}
	return base64HeaderValue(value)
}

func base64HeaderValue(value string) string {
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}
