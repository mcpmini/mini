//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSession_configureIndependentTools(t *testing.T) {
	client := quickServer(t, map[string]string{
		"tool_a": `{"id":1,"secret":"hidden","title":"a"}`,
		"tool_b": `{"id":2,"secret":"visible","title":"b"}`,
	})
	client.setProjection("svc", "tool_a", map[string]any{"exclude": []string{"secret"}}, true)

	bA, _ := json.Marshal(client.execEnvelope("svc", "tool_a", nil).Data)
	if strings.Contains(string(bA), "secret") {
		t.Errorf("tool_a: secret should be excluded, got: %s", bA)
	}

	bB, _ := json.Marshal(client.execEnvelope("svc", "tool_b", nil).Data)
	if !strings.Contains(string(bB), "secret") {
		t.Errorf("tool_b: secret should be visible (different tool config), got: %s", bB)
	}
}

func TestSession_multipleExecCallsShareProjection(t *testing.T) {
	client := quickServer(t, map[string]string{"get_item": `{"id":1,"title":"hello","noise":"strip this"}`})
	client.setProjection("svc", "get_item", map[string]any{"include_only": []string{"id", "title"}}, true)

	for i := range 3 {
		b, _ := json.Marshal(client.execEnvelope("svc", "get_item", nil).Data)
		if strings.Contains(string(b), "noise") {
			t.Errorf("call %d: 'noise' should be excluded by sticky projection", i+1)
		}
	}
}
