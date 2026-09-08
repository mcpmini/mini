package toon

import (
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
