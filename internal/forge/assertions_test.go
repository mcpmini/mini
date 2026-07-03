//go:build test

package forge_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("got is not valid JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantVal); err != nil {
		t.Fatalf("want is not valid JSON: %v (%s)", err, want)
	}
	gotJSON, _ := json.Marshal(gotVal)
	wantJSON, _ := json.Marshal(wantVal)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
