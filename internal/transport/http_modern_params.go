package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/mcpmini/mini/internal/version"
)

func (c *HTTPConnection) prepareModernParams(params json.RawMessage, modernVersion string) (json.RawMessage, error) {
	fields, err := decodeObjectParams(params)
	if err != nil {
		return nil, err
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := fields["_meta"]; ok {
		if err = json.Unmarshal(raw, &meta); err != nil || meta == nil {
			return nil, fmt.Errorf("modern request _meta must be a JSON object")
		}
	}
	if err = addDefaultMeta(meta, modernVersion); err != nil {
		return nil, err
	}
	if fields["_meta"], err = json.Marshal(meta); err != nil {
		return nil, fmt.Errorf("encode modern request metadata: %w", err)
	}
	return json.Marshal(fields)
}

func addDefaultMeta(meta map[string]json.RawMessage, modernVersion string) error {
	encoded, err := json.Marshal(RequestMeta{
		ProtocolVersion:    modernVersion,
		ClientInfo:         &ClientInfo{Name: "mini", Version: version.Version},
		ClientCapabilities: &ClientCapabilities{},
	})
	if err != nil {
		return fmt.Errorf("encode modern request metadata: %w", err)
	}
	var defaults map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &defaults); err != nil {
		return fmt.Errorf("decode modern request metadata: %w", err)
	}
	for key, value := range defaults {
		if _, ok := meta[key]; !ok {
			meta[key] = value
		}
	}
	return nil
}

func decodeObjectParams(params json.RawMessage) (map[string]json.RawMessage, error) {
	if len(params) == 0 || string(bytes.TrimSpace(params)) == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("modern request params must be a JSON object")
	}
	return fields, nil
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
