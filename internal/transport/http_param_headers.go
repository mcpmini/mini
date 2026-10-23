package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
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

func modernParamString(fields map[string]json.RawMessage, key string) (string, bool) {
	var value string
	raw, ok := fields[key]
	if !ok || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func (c *HTTPConnection) addToolCallHeaders(req *http.Request, params json.RawMessage) {
	fields, err := decodeObjectParams(params)
	if err != nil {
		return
	}
	name, ok := modernParamString(fields, "name")
	if !ok {
		return
	}
	req.Header.Set("Mcp-Name", encodeModernHeaderValue(name))
	var args map[string]json.RawMessage
	if json.Unmarshal(fields["arguments"], &args) != nil {
		return
	}
	c.modernMu.RLock()
	defer c.modernMu.RUnlock()
	for _, header := range c.modernHeaders[name] {
		if value, ok := argumentAtPath(args, header.path); ok {
			req.Header.Set("Mcp-Param-"+header.name, encodeModernHeaderValue(value))
		}
	}
}

func argumentAtPath(args map[string]json.RawMessage, path []string) (string, bool) {
	for i, key := range path {
		raw, ok := args[key]
		if !ok {
			return "", false
		}
		if i == len(path)-1 {
			return primitiveHeaderValue(raw)
		}
		var child map[string]json.RawMessage
		if json.Unmarshal(raw, &child) != nil || child == nil {
			return "", false
		}
		args = child
	}
	return "", false
}

func primitiveHeaderValue(raw json.RawMessage) (string, bool) {
	value, ok := decodeHeaderValue(raw)
	if !ok {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case json.Number:
		return integerHeaderValue(typed)
	default:
		return "", false
	}
}

func decodeHeaderValue(raw json.RawMessage) (any, bool) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	return value, true
}

func integerHeaderValue(value json.Number) (string, bool) {
	rational, ok := new(big.Rat).SetString(string(value))
	if !ok || !rational.IsInt() || !rational.Num().IsInt64() {
		return "", false
	}
	integer := rational.Num().Int64()
	if integer < -9007199254740991 || integer > 9007199254740991 {
		return "", false
	}
	return strconv.FormatInt(integer, 10), true
}
