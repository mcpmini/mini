//go:build test

package response_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/response"
)

func TestEnvelope_NullSuccessDataStillSerializesDataKey(t *testing.T) {
	env := &response.Envelope{}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"data":null}` {
		t.Errorf("got %s, want data:null preserved for a successful null result", got)
	}
}

func TestEnvelope_ErrorNeverIncludesDataKey(t *testing.T) {
	env := response.BuildError(response.BuildErrorParams{Code: "tool_error", Message: "boom"})
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["data"]; ok {
		t.Errorf("error envelope must not include a data key at all, got: %s", b)
	}
}
