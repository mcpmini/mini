package toon

import (
	"encoding/json"
	"testing"
)

func TestFromJSONPreservesDocumentKeyOrder(t *testing.T) {
	raw := json.RawMessage(`{"zebra":1,"apple":2,"mango":3}`)
	v, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	want := []string{"zebra", "apple", "mango"}
	if len(v.Fields) != len(want) {
		t.Fatalf("got %d fields, want %d", len(v.Fields), len(want))
	}
	for i, k := range want {
		if v.Fields[i].Key != k {
			t.Errorf("field %d key = %q, want %q", i, v.Fields[i].Key, k)
		}
	}

	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	wantDoc := "zebra: 1\napple: 2\nmango: 3"
	if got != wantDoc {
		t.Errorf("Encode() = %q, want %q", got, wantDoc)
	}
}

func TestFromJSONScalarKinds(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		kind Kind
	}{
		{"null", `null`, KindNull},
		{"bool true", `true`, KindBool},
		{"bool false", `false`, KindBool},
		{"number", `42`, KindNumber},
		{"string", `"hello"`, KindString},
		{"object", `{}`, KindObject},
		{"array", `[]`, KindArray},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := FromJSON(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("FromJSON(%s) unexpected error: %v", tc.raw, err)
			}
			if v.Kind != tc.kind {
				t.Errorf("FromJSON(%s).Kind = %v, want %v", tc.raw, v.Kind, tc.kind)
			}
		})
	}
}

func TestFromJSONPopulatesArrayItemsFully(t *testing.T) {
	raw := json.RawMessage(`{"items":[1,"two",true,null,{"k":3}]}`)
	v, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	items := v.Fields[0].Val
	if items.Kind != KindArray {
		t.Fatalf("items.Kind = %v, want KindArray", items.Kind)
	}
	if len(items.Items) != 5 {
		t.Fatalf("len(items.Items) = %d, want 5", len(items.Items))
	}
	wantKinds := []Kind{KindNumber, KindString, KindBool, KindNull, KindObject}
	for i, k := range wantKinds {
		if items.Items[i].Kind != k {
			t.Errorf("items.Items[%d].Kind = %v, want %v", i, items.Items[i].Kind, k)
		}
	}
	if items.Items[1].Str != "two" {
		t.Errorf("items.Items[1].Str = %q, want %q", items.Items[1].Str, "two")
	}
	if items.Items[4].Fields[0].Key != "k" || items.Items[4].Fields[0].Val.Num != "3" {
		t.Errorf("items.Items[4] nested object decoded incorrectly: %+v", items.Items[4])
	}
}

func TestFromJSONBigIntegerSurvivesDigitExact(t *testing.T) {
	raw := json.RawMessage(`{"bignum":123456789012345678901234567890}`)
	v, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	got := v.Fields[0].Val.Num
	want := "123456789012345678901234567890"
	if got != want {
		t.Errorf("Num = %q, want %q", got, want)
	}
}

func TestFromJSONMalformedInputErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"unterminated object", `{"a":1`},
		{"garbage token", `{a:1}`},
		{"non-string object key", `{1:2}`},
		{"trailing data", `1 2`},
		{"empty input", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := FromJSON(json.RawMessage(tc.raw)); err == nil {
				t.Errorf("FromJSON(%q) expected error, got nil", tc.raw)
			}
		})
	}
}
