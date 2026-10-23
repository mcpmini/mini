//go:build test

package server_test

import (
	"encoding/json"
	"testing"
)

func deepPayload(t *testing.T) string {
	t.Helper()
	value := map[string]any{"depth-sentinel": "must-not-be-serialized"}
	for i := 0; i < 1025; i++ {
		value = map[string]any{"nested": value, "sibling": i}
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal deep payload: %v", err)
	}
	text, err := json.Marshal(string(data))
	if err != nil {
		t.Fatalf("marshal response text: %v", err)
	}
	return string(text)
}
