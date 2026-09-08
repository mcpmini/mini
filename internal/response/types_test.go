//go:build test

package response_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

func TestNewProxyResult_UnalteredEnvelopeOmitsMini(t *testing.T) {
	env := &response.Envelope{Data: map[string]any{"id": 1}}
	pr := response.NewProxyResult(env)
	if pr.Mini != nil {
		t.Errorf("expected nil Mini for unaltered envelope, got: %+v", pr.Mini)
	}
	b, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"data":{"id":1}}` {
		t.Errorf("got %s, want no __mini key", got)
	}
}

func TestNewProxyResult_NilDataStillSerializesDataKey(t *testing.T) {
	env := &response.Envelope{}
	pr := response.NewProxyResult(env)
	b, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"data":null}` {
		t.Errorf("got %s, want data:null preserved", got)
	}
}

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
	env := response.BuildError("tool_error", "boom", false, "")
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

func TestNewProxyResult_ExcludedFieldsPopulateMiniWithMsg(t *testing.T) {
	env := &response.Envelope{Data: map[string]any{"id": 1}, Excluded: []string{".secret"}}
	pr := response.NewProxyResult(env)
	if pr.Mini == nil {
		t.Fatal("expected Mini to be set when fields excluded")
	}
	if pr.Mini.Msg != response.ProxyMsg {
		t.Errorf("Msg = %q, want %q", pr.Mini.Msg, response.ProxyMsg)
	}
	if len(pr.Mini.Excluded) != 1 || pr.Mini.Excluded[0] != ".secret" {
		t.Errorf("Excluded = %v", pr.Mini.Excluded)
	}
}

func TestNewProxyResult_TruncatedFieldsPopulateMiniWithMsg(t *testing.T) {
	env := &response.Envelope{
		Data:      map[string]any{"body": "abc"},
		Truncated: []projection.Truncation{{JQPath: ".body", Chars: 100}},
	}
	pr := response.NewProxyResult(env)
	if pr.Mini == nil {
		t.Fatal("expected Mini to be set when fields truncated")
	}
	if pr.Mini.Msg != response.ProxyMsg {
		t.Errorf("Msg = %q, want %q", pr.Mini.Msg, response.ProxyMsg)
	}
	if len(pr.Mini.Truncated) != 1 || pr.Mini.Truncated[0].JQPath != ".body" {
		t.Errorf("Truncated = %v", pr.Mini.Truncated)
	}
}

func TestNewProxyResult_FileAloneDoesNotSetMini(t *testing.T) {
	key := "1234567890"
	env := &response.Envelope{Data: map[string]any{"id": 1}, File: &key}
	pr := response.NewProxyResult(env)
	if pr.Mini != nil {
		t.Errorf("file alone without excluded/truncated must not trigger __mini, got: %+v", pr.Mini)
	}
}

func TestNewProxyResult_PassthroughAloneDoesNotSetMini(t *testing.T) {
	env := &response.Envelope{Data: map[string]any{"id": 1}, Passthrough: map[string]any{"cursor": "abc"}}
	pr := response.NewProxyResult(env)
	if pr.Mini != nil {
		t.Errorf("passthrough alone must not trigger __mini (completeness signal), got: %+v", pr.Mini)
	}
}

func TestNewProxyResult_PassthroughIncludedWhenAltered(t *testing.T) {
	env := &response.Envelope{
		Data:        map[string]any{"id": 1},
		Excluded:    []string{".secret"},
		Passthrough: map[string]any{"cursor": "abc"},
	}
	pr := response.NewProxyResult(env)
	if pr.Mini == nil {
		t.Fatal("expected Mini when fields excluded")
	}
	if pr.Mini.Passthrough["cursor"] != "abc" {
		t.Errorf("passthrough should be included when __mini is present, got: %v", pr.Mini.Passthrough)
	}
}

func TestNewProxyResult_MarshalsOnceForAllFields(t *testing.T) {
	key := "42"
	env := &response.Envelope{
		Data:        []any{1, 2, 3},
		Excluded:    []string{".a"},
		Truncated:   []projection.Truncation{{JQPath: ".b", Items: 5}},
		Passthrough: map[string]any{"cursor": "x"},
		File:        &key,
	}
	pr := response.NewProxyResult(env)
	b, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatal(err)
	}
	if round["data"] == nil {
		t.Error("expected data in marshaled result")
	}
	mini, _ := round["__mini"].(map[string]any)
	if mini == nil {
		t.Fatal("expected __mini in marshaled result")
	}
	for _, key := range []string{"msg", "file", "excluded", "truncated", "passthrough"} {
		if mini[key] == nil {
			t.Errorf("expected __mini.%s to be set", key)
		}
	}
}

func TestEnvelopeWireMapMatchesMarshalJSON(t *testing.T) {
	key := "12345"
	cases := []struct {
		name string
		env  response.Envelope
	}{
		{"success minimal", response.Envelope{Data: "hello"}},
		{"success full", response.Envelope{
			Data:        map[string]any{"id": 1},
			Excluded:    []string{".secret"},
			Truncated:   []projection.Truncation{{JQPath: ".body", Chars: 42}},
			File:        &key,
			Passthrough: map[string]any{"cursor": "abc"},
		}},
		{"error minimal", response.Envelope{Error: "not_found"}},
		{"error full", response.Envelope{
			Error: "rate_limited", Message: "try later",
			Retryable: true, Action: "retry_after_30s",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jsonBytes, err := json.Marshal(tc.env)
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			wireBytes, err := json.Marshal(tc.env.WireMap())
			if err != nil {
				t.Fatalf("Marshal(WireMap): %v", err)
			}
			var fromJSON, fromWire map[string]any
			if err := json.Unmarshal(jsonBytes, &fromJSON); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wireBytes, &fromWire); err != nil {
				t.Fatal(err)
			}
			if !jsonEqual(fromJSON, fromWire) {
				t.Errorf("MarshalJSON and WireMap diverge:\n  JSON: %s\n  Wire: %s", jsonBytes, wireBytes)
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
