package transport

import (
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

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
