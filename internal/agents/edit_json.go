package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// EditJSONServers removes and adds servers, preserving an existing mini entry.
func EditJSONServers(data []byte, remove []string, add map[string]any) ([]byte, error) {
	doc, err := decodeJSONObject(data)
	if err != nil {
		return nil, err
	}
	servers, err := mcpServersOf(doc)
	if err != nil {
		return nil, err
	}
	editJSONEntries(servers, remove, add)
	doc["mcpServers"] = servers
	return encodeJSON(doc)
}

func editJSONEntries(servers map[string]any, remove []string, add map[string]any) {
	for _, name := range remove {
		if name != MiniKey {
			delete(servers, name)
		}
	}
	for name, entry := range add {
		if _, exists := servers[name]; name != MiniKey || !exists {
			servers[name] = entry
		}
	}
}

func decodeJSONObject(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse agent config: %w", err)
	}
	if doc == nil {
		return nil, errors.New("parse agent config: not a JSON object")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse agent config: unexpected data after the JSON object")
	}
	return doc, nil
}

func mcpServersOf(doc map[string]any) (map[string]any, error) {
	raw, ok := doc["mcpServers"]
	if !ok || raw == nil {
		return map[string]any{}, nil
	}
	servers, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("agent config's mcpServers is not an object")
	}
	return servers, nil
}

func encodeJSON(doc map[string]any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
