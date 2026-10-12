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
	if len(v.fields()) != len(want) {
		t.Fatalf("got %d fields, want %d", len(v.fields()), len(want))
	}
	for i, k := range want {
		if v.fields()[i].Key != k {
			t.Errorf("field %d key = %q, want %q", i, v.fields()[i].Key, k)
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
		kind valueKind
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
			if v.kind() != tc.kind {
				t.Errorf("FromJSON(%s).kind() = %v, want %v", tc.raw, v.kind(), tc.kind)
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
	items := v.fields()[0].Val
	if items.kind() != KindArray {
		t.Fatalf("items.kind() = %v, want KindArray", items.kind())
	}
	if len(items.items()) != 5 {
		t.Fatalf("len(items.items()) = %d, want 5", len(items.items()))
	}
	wantKinds := []valueKind{KindNumber, KindString, KindBool, KindNull, KindObject}
	for i, k := range wantKinds {
		if items.items()[i].kind() != k {
			t.Errorf("items.items()[%d].kind() = %v, want %v", i, items.items()[i].kind(), k)
		}
	}
	if items.items()[1].payload != stringValue("two") {
		t.Errorf("second item payload = %v, want %q", items.items()[1].payload, "two")
	}
	if items.items()[4].fields()[0].Key != "k" || items.items()[4].fields()[0].Val.payload != numberValue("3") {
		t.Errorf("items.items()[4] nested object decoded incorrectly: %+v", items.items()[4])
	}
}

func TestFromJSONBigIntegerSurvivesDigitExact(t *testing.T) {
	raw := json.RawMessage(`{"bignum":123456789012345678901234567890}`)
	v, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	got := v.fields()[0].Val.payload
	want := "123456789012345678901234567890"
	if got != numberValue(want) {
		t.Errorf("number payload = %v, want %q", got, want)
	}
}

func TestFromJSONDuplicateKeyLastWinsFirstPosition(t *testing.T) {
	raw := json.RawMessage(`{"a":1,"b":2,"a":3}`)
	v, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	if len(v.fields()) != 2 {
		t.Fatalf("got %d fields, want 2 (duplicate key collapsed)", len(v.fields()))
	}
	if v.fields()[0].Key != "a" || v.fields()[0].Val.payload != numberValue("3") {
		t.Errorf("fields[0] = {%q, %v}, want {a, 3}", v.fields()[0].Key, v.fields()[0].Val.payload)
	}
	if v.fields()[1].Key != "b" || v.fields()[1].Val.payload != numberValue("2") {
		t.Errorf("fields[1] = {%q, %v}, want {b, 2}", v.fields()[1].Key, v.fields()[1].Val.payload)
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
