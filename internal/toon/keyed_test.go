package toon

import (
	"fmt"
	"strings"
	"testing"
)

func TestKeyedTabularColsRequiresMinTwoEntries(t *testing.T) {
	single := objVal(Field{Key: "only", Val: objVal(Field{Key: "a", Val: numVal("1")})})
	if _, ok := keyedTabularCols(single); ok {
		t.Error("keyedTabularCols returned ok for 1-entry object, want false")
	}
}

func TestEncodeKeyedTabularRootForm(t *testing.T) {
	v := objVal(
		Field{Key: "alpha", Val: objVal(Field{Key: "x", Val: numVal("1")}, Field{Key: "y", Val: numVal("2")})},
		Field{Key: "beta", Val: objVal(Field{Key: "x", Val: numVal("3")}, Field{Key: "y", Val: numVal("4")})},
	)
	want := "[2:]{x,y}:\n  alpha: 1,2\n  beta: 3,4"
	if got := encodeOK(t, v); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeKeyedTabularHashKey(t *testing.T) {
	v := objVal(
		Field{Key: "#branch", Val: objVal(Field{Key: "sha", Val: strVal("abc")})},
		Field{Key: "main", Val: objVal(Field{Key: "sha", Val: strVal("def")})},
	)
	want := "[2:]{sha}:\n  \"#branch\": abc\n  main: def"
	if got := encodeOK(t, v); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeKeyedTabularCommaInCellValue(t *testing.T) {
	v := objVal(
		Field{Key: "a", Val: objVal(Field{Key: "tag", Val: strVal("foo,bar")})},
		Field{Key: "b", Val: objVal(Field{Key: "tag", Val: strVal("baz")})},
	)
	want := "[2:]{tag}:\n  a: \"foo,bar\"\n  b: baz"
	if got := encodeOK(t, v); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestKeyedTabularNonUniformFallsThrough(t *testing.T) {
	v := objVal(Field{Key: "data", Val: objVal(
		Field{Key: "a", Val: objVal(Field{Key: "x", Val: numVal("1")})},
		Field{Key: "b", Val: objVal(Field{Key: "y", Val: numVal("2")})},
	)})
	got := encodeOK(t, v)
	if strings.Contains(got, ":]{") {
		t.Errorf("non-uniform entries must not use keyed tabular, got: %s", got)
	}
}

func TestKeyedTabularAsObjectField(t *testing.T) {
	v := objVal(
		Field{Key: "meta", Val: strVal("info")},
		Field{Key: "users", Val: objVal(
			Field{Key: "alice", Val: objVal(Field{Key: "age", Val: numVal("30")})},
			Field{Key: "bob", Val: objVal(Field{Key: "age", Val: numVal("25")})},
		)},
	)
	want := "meta: info\nusers[2:]{age}:\n  alice: 30\n  bob: 25"
	if got := encodeOK(t, v); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeKeyedTabularNestedFieldGroup(t *testing.T) {
	v := objVal(
		Field{Key: "alpha", Val: objVal(Field{Key: "pos", Val: objVal(
			Field{Key: "x", Val: numVal("1")},
			Field{Key: "y", Val: numVal("2")},
		)})},
		Field{Key: "beta", Val: objVal(Field{Key: "pos", Val: objVal(
			Field{Key: "x", Val: numVal("3")},
			Field{Key: "y", Val: numVal("4")},
		)})},
	)
	want := "[2:]{pos{x,y}}:\n  alpha: 1,2\n  beta: 3,4"
	if got := encodeOK(t, v); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestKeyedTabularEmptyInnerObjectFallsThrough(t *testing.T) {
	v := objVal(
		Field{Key: "a", Val: objVal()},
		Field{Key: "b", Val: objVal()},
	)
	got := encodeOK(t, v)
	if strings.Contains(got, ":]{") {
		t.Errorf("empty inner objects must not trigger keyed tabular, got: %s", got)
	}
}

func TestEncodeKeyedTabularMixedPrimitiveTypes(t *testing.T) {
	v := objVal(
		Field{Key: "a", Val: objVal(Field{Key: "v", Val: numVal("1")})},
		Field{Key: "b", Val: objVal(Field{Key: "v", Val: strVal("hello")})},
		Field{Key: "c", Val: objVal(Field{Key: "v", Val: boolVal(true)})},
		Field{Key: "d", Val: objVal(Field{Key: "v", Val: nullVal()})},
	)
	want := "[4:]{v}:\n  a: 1\n  b: hello\n  c: true\n  d: null"
	if got := encodeOK(t, v); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeKeyedTabularLargeEntryCount(t *testing.T) {
	fields := make([]Field, 12)
	for i := range fields {
		fields[i] = Field{
			Key: fmt.Sprintf("key%d", i),
			Val: objVal(Field{Key: "n", Val: numVal(fmt.Sprintf("%d", i))}),
		}
	}
	v := objVal(fields...)
	got := encodeOK(t, v)
	if !strings.Contains(got, "[12:]{n}:") {
		t.Errorf("Encode() = %q, want keyed tabular header [12:]{n}:", got)
	}
}

func TestWriteKeyedTabularDepthError(t *testing.T) {
	var sb strings.Builder
	obj := objVal(
		Field{Key: "a", Val: objVal(Field{Key: "x", Val: numVal("1")})},
		Field{Key: "b", Val: objVal(Field{Key: "x", Val: numVal("2")})},
	)
	cols, ok := keyedTabularCols(obj)
	if !ok {
		t.Fatal("keyedTabularCols returned false")
	}
	if err := writeKeyedTabular(&sb, "k", obj, cols, maxEncodeDepth+1); err == nil {
		t.Fatal("expected depth error, got nil")
	}
}

func TestEncodeKeyedTabularInvalidColumnKeyError(t *testing.T) {
	invalid := string([]byte{0xff})
	v := objVal(
		Field{Key: "a", Val: objVal(Field{Key: invalid, Val: numVal("1")})},
		Field{Key: "b", Val: objVal(Field{Key: invalid, Val: numVal("2")})},
	)
	_, err := Encode(v)
	if err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("Encode() error = %v, want invalid UTF-8 error", err)
	}
}

func TestEncodeKeyedTabularInvalidEntryKeyError(t *testing.T) {
	invalid := string([]byte{0xff})
	v := objVal(
		Field{Key: "a", Val: objVal(Field{Key: "x", Val: numVal("1")})},
		Field{Key: invalid, Val: objVal(Field{Key: "x", Val: numVal("2")})},
	)
	_, err := Encode(v)
	if err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("Encode() error = %v, want invalid UTF-8 error", err)
	}
}
