package toon

import "testing"

func strVal(s string) Value   { return Value{Kind: KindString, Str: s} }
func numVal(n string) Value   { return Value{Kind: KindNumber, Num: n} }
func boolVal(b bool) Value    { return Value{Kind: KindBool, Bool: b} }
func nullVal() Value          { return Value{Kind: KindNull} }
func objVal(f ...Field) Value { return Value{Kind: KindObject, Fields: f} }

func TestEncodeRootScalars(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"root string bare", strVal("hello"), "hello"},
		{"root string needs quoting", strVal("42"), `"42"`},
		{"root number", numVal("42"), "42"},
		{"root bool true", boolVal(true), "true"},
		{"root bool false", boolVal(false), "false"},
		{"root null", nullVal(), "null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.v)
			if err != nil {
				t.Fatalf("Encode unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEncodeEmptyRootObjectYieldsEmptyDocument(t *testing.T) {
	got, err := Encode(objVal())
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("Encode(empty object) = %q, want empty document", got)
	}
}

func TestEncodeObjectPrimitiveFields(t *testing.T) {
	v := objVal(
		Field{Key: "id", Val: numVal("123")},
		Field{Key: "name", Val: strVal("Ada")},
		Field{Key: "active", Val: boolVal(true)},
	)
	want := "id: 123\nname: Ada\nactive: true"
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeNestedObject(t *testing.T) {
	v := objVal(
		Field{Key: "user", Val: objVal(
			Field{Key: "id", Val: numVal("123")},
			Field{Key: "name", Val: strVal("Ada")},
		)},
	)
	want := "user:\n  id: 123\n  name: Ada"
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeEmptyNestedObjectField(t *testing.T) {
	v := objVal(
		Field{Key: "meta", Val: objVal()},
		Field{Key: "id", Val: numVal("1")},
	)
	want := "meta:\nid: 1"
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeObjectKeyRequiringQuoting(t *testing.T) {
	v := objVal(Field{Key: "my-key", Val: strVal("v")})
	want := `"my-key": v`
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeNoTrailingNewlineOrSpaces(t *testing.T) {
	v := objVal(
		Field{Key: "a", Val: numVal("1")},
		Field{Key: "b", Val: objVal(Field{Key: "c", Val: numVal("2")})},
	)
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if len(got) > 0 && got[len(got)-1] == '\n' {
		t.Errorf("Encode() ended with trailing newline: %q", got)
	}
	for _, line := range splitLines(got) {
		if len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t') {
			t.Errorf("line %q has trailing whitespace", line)
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func TestEncodeUnknownKindErrors(t *testing.T) {
	if _, err := Encode(Value{}); err == nil {
		t.Error("Encode(zero-value Value) expected error for unknown kind, got nil")
	}
	if _, err := Encode(Value{Kind: Kind(99)}); err == nil {
		t.Error("Encode(out-of-range Kind) expected error, got nil")
	}
}

func TestEncodeObjectFieldWithUnknownKindErrors(t *testing.T) {
	v := objVal(Field{Key: "x", Val: Value{Kind: Kind(99)}})
	if _, err := Encode(v); err == nil {
		t.Error("Encode(object field with unknown kind) expected error, got nil")
	}
}

// Two fields per level so §13.4 safe folding cannot collapse the chain.
func nestedObj(depth int) Value {
	v := objVal(Field{Key: "leaf", Val: numVal("1")}, Field{Key: "z", Val: numVal("2")})
	for range depth {
		v = objVal(Field{Key: "n", Val: v}, Field{Key: "x", Val: numVal("2")})
	}
	return v
}

func nestedListArray(depth int) Value {
	v := Value{Kind: KindArray, Items: []Value{objVal(Field{Key: "a", Val: numVal("1")}, Field{Key: "b", Val: objVal()})}}
	for range depth {
		v = Value{Kind: KindArray, Items: []Value{v, numVal("1")}}
	}
	return v
}

func TestEncodeDepthCap(t *testing.T) {
	t.Run("object nesting beyond cap errors", func(t *testing.T) {
		if _, err := Encode(nestedObj(maxEncodeDepth + 1)); err == nil {
			t.Error("expected depth error, got nil")
		}
	})
	t.Run("object nesting at cap encodes", func(t *testing.T) {
		if _, err := Encode(nestedObj(maxEncodeDepth - 1)); err != nil {
			t.Errorf("expected success at cap, got: %v", err)
		}
	})
	t.Run("array-of-array nesting beyond cap errors", func(t *testing.T) {
		if _, err := Encode(nestedListArray(maxEncodeDepth + 1)); err == nil {
			t.Error("expected depth error, got nil")
		}
	})
}
