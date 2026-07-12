package toon

import "testing"

func arrVal(items ...Value) Value { return Value{Kind: KindArray, Items: items} }

func encodeOK(t *testing.T, v Value) string {
	t.Helper()
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	return got
}

func TestEncodeInlineArray(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"strings", objVal(Field{Key: "tags", Val: arrVal(strVal("reading"), strVal("gaming"))}), "tags[2]: reading,gaming"},
		{"numbers", objVal(Field{Key: "nums", Val: arrVal(numVal("1"), numVal("2"), numVal("3"))}), "nums[3]: 1,2,3"},
		{"mixed primitives", objVal(Field{Key: "data", Val: arrVal(strVal("x"), boolVal(true), numVal("10"), nullVal())}), "data[4]: x,true,10,null"},
		{"quotes comma and colon values", objVal(Field{Key: "items", Val: arrVal(strVal("a"), strVal("b,c"), strVal("d:e"))}), `items[3]: a,"b,c","d:e"`},
		{"quotes ambiguous literals", objVal(Field{Key: "items", Val: arrVal(strVal("true"), strVal("42"))}), `items[2]: "true","42"`},
		{"empty string item", objVal(Field{Key: "items", Val: arrVal(strVal("a"), strVal(""), strVal("b"))}), `items[3]: a,"",b`},
		{"whitespace-only items", objVal(Field{Key: "items", Val: arrVal(strVal(" "), strVal("  "))}), `items[2]: " ","  "`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeOK(t, tc.v); got != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEncodeEmptyArrayForms(t *testing.T) {
	t.Run("object field emits key colon brackets", func(t *testing.T) {
		v := objVal(Field{Key: "items", Val: arrVal()})
		if got := encodeOK(t, v); got != "items: []" {
			t.Errorf("Encode() = %q, want %q", got, "items: []")
		}
	})
	t.Run("root emits bare brackets", func(t *testing.T) {
		if got := encodeOK(t, arrVal()); got != "[]" {
			t.Errorf("Encode() = %q, want %q", got, "[]")
		}
	})
	t.Run("inner list item emits zero header", func(t *testing.T) {
		v := objVal(Field{Key: "pairs", Val: arrVal(arrVal(), arrVal())})
		want := "pairs[2]:\n  - [0]:\n  - [0]:"
		if got := encodeOK(t, v); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
	t.Run("empty array first field stays on hyphen line", func(t *testing.T) {
		v := objVal(Field{Key: "items", Val: arrVal(
			objVal(Field{Key: "data", Val: arrVal()}, Field{Key: "name", Val: strVal("x")}),
		)})
		want := "items[1]:\n  - data: []\n    name: x"
		if got := encodeOK(t, v); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
}

func TestEncodeTabularArray(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{
			"uniform objects",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "sku", Val: strVal("A1")}, Field{Key: "qty", Val: numVal("2")}, Field{Key: "price", Val: numVal("9.99")}),
				objVal(Field{Key: "sku", Val: strVal("B2")}, Field{Key: "qty", Val: numVal("1")}, Field{Key: "price", Val: numVal("14.5")}),
			)}),
			"items[2]{sku,qty,price}:\n  A1,2,9.99\n  B2,1,14.5",
		},
		{
			"null values in rows",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "id", Val: numVal("1")}, Field{Key: "value", Val: nullVal()}),
				objVal(Field{Key: "id", Val: numVal("2")}, Field{Key: "value", Val: strVal("test")}),
			)}),
			"items[2]{id,value}:\n  1,null\n  2,test",
		},
		{
			"quotes delimiter and ambiguous cells",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "sku", Val: strVal("A,1")}, Field{Key: "status", Val: strVal("true")}),
				objVal(Field{Key: "sku", Val: strVal("B2")}, Field{Key: "status", Val: strVal("wip: x")}),
			)}),
			"items[2]{sku,status}:\n  \"A,1\",\"true\"\n  B2,\"wip: x\"",
		},
		{
			"key order follows first object with lookup for later rows",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "a", Val: numVal("1")}, Field{Key: "b", Val: numVal("2")}, Field{Key: "c", Val: numVal("3")}),
				objVal(Field{Key: "c", Val: numVal("30")}, Field{Key: "b", Val: numVal("20")}, Field{Key: "a", Val: numVal("10")}),
			)}),
			"items[2]{a,b,c}:\n  1,2,3\n  10,20,30",
		},
		{
			"field names requiring quotes",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "order:id", Val: numVal("1")}, Field{Key: "full name", Val: strVal("Ada")}),
				objVal(Field{Key: "order:id", Val: numVal("2")}, Field{Key: "full name", Val: strVal("Bob")}),
			)}),
			"items[2]{\"order:id\",\"full name\"}:\n  1,Ada\n  2,Bob",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeOK(t, tc.v); got != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEncodeTabularIneligibleFallsBackToList(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{
			"mismatched key sets",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "id", Val: numVal("1")}, Field{Key: "name", Val: strVal("First")}),
				objVal(Field{Key: "id", Val: numVal("2")}, Field{Key: "extra", Val: boolVal(true)}),
			)}),
			"items[2]:\n  - id: 1\n    name: First\n  - id: 2\n    extra: true",
		},
		{
			"non-primitive value in element",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "id", Val: numVal("1")}, Field{Key: "tags", Val: arrVal(strVal("a"))}),
				objVal(Field{Key: "id", Val: numVal("2")}, Field{Key: "tags", Val: arrVal(strVal("b"))}),
			)}),
			"items[2]:\n  - id: 1\n    tags[1]: a\n  - id: 2\n    tags[1]: b",
		},
		{
			"mixed element kinds",
			objVal(Field{Key: "items", Val: arrVal(
				numVal("1"),
				objVal(Field{Key: "a", Val: numVal("1")}),
				strVal("text"),
			)}),
			"items[3]:\n  - 1\n  - a: 1\n  - text",
		},
		{
			"empty object element",
			objVal(Field{Key: "items", Val: arrVal(
				strVal("first"),
				strVal("second"),
				objVal(),
			)}),
			"items[3]:\n  - first\n  - second\n  -",
		},
		{
			"all elements empty objects",
			objVal(Field{Key: "items", Val: arrVal(objVal(), objVal())}),
			"items[2]:\n  -\n  -",
		},
		{
			"duplicate keys in an element",
			objVal(Field{Key: "items", Val: arrVal(
				objVal(Field{Key: "id", Val: numVal("1")}, Field{Key: "id", Val: numVal("2")}),
				objVal(Field{Key: "id", Val: numVal("3")}, Field{Key: "id", Val: numVal("4")}),
			)}),
			"items[2]:\n  - id: 1\n    id: 2\n  - id: 3\n    id: 4",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeOK(t, tc.v); got != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEncodeListArrayNestedForms(t *testing.T) {
	t.Run("arrays of arrays", func(t *testing.T) {
		v := objVal(Field{Key: "pairs", Val: arrVal(
			arrVal(strVal("a"), strVal("b")),
			arrVal(strVal("c,d"), strVal("true")),
		)})
		want := "pairs[2]:\n  - [2]: a,b\n  - [2]: \"c,d\",\"true\""
		if got := encodeOK(t, v); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
	t.Run("uniform object array in list-item position uses expanded list not tabular", func(t *testing.T) {
		v := objVal(Field{Key: "items", Val: arrVal(
			strVal("summary"),
			arrVal(
				objVal(Field{Key: "id", Val: numVal("2")}),
				objVal(Field{Key: "id", Val: numVal("3")}),
			),
		)})
		want := "items[2]:\n  - summary\n  - [2]:\n    - id: 2\n    - id: 3"
		if got := encodeOK(t, v); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
	t.Run("list item object with leading list-form array puts items at plus two", func(t *testing.T) {
		v := objVal(Field{Key: "items", Val: arrVal(
			objVal(
				Field{Key: "matrix", Val: arrVal(arrVal(numVal("1"), numVal("2")), arrVal(numVal("3"), numVal("4")))},
				Field{Key: "name", Val: strVal("grid")},
			),
		)})
		want := "items[1]:\n  - matrix[2]:\n      - [2]: 1,2\n      - [2]: 3,4\n    name: grid"
		if got := encodeOK(t, v); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
	t.Run("first field inline array shares hyphen line", func(t *testing.T) {
		v := objVal(Field{Key: "items", Val: arrVal(
			objVal(
				Field{Key: "nums", Val: arrVal(numVal("1"), numVal("2"), numVal("3"))},
				Field{Key: "name", Val: strVal("Ada")},
			),
		)})
		want := "items[1]:\n  - nums[3]: 1,2,3\n    name: Ada"
		if got := encodeOK(t, v); got != want {
			t.Errorf("Encode() = %q, want %q", got, want)
		}
	})
}

func TestListItemLeadingTabularArrayDepth(t *testing.T) {
	v := objVal(Field{Key: "items", Val: arrVal(
		objVal(
			Field{Key: "users", Val: arrVal(
				objVal(Field{Key: "id", Val: numVal("1")}, Field{Key: "name", Val: strVal("Ada")}),
				objVal(Field{Key: "id", Val: numVal("2")}, Field{Key: "name", Val: strVal("Bob")}),
			)},
			Field{Key: "status", Val: strVal("active")},
		),
	)})
	want := "items[1]:\n  - users[2]{id,name}:\n      1,Ada\n      2,Bob\n    status: active"
	if got := encodeOK(t, v); got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestEncodeRootArrayForms(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"inline", arrVal(strVal("x"), strVal("true"), boolVal(true), numVal("10")), `[4]: x,"true",true,10`},
		{"tabular", arrVal(objVal(Field{Key: "id", Val: numVal("1")}), objVal(Field{Key: "id", Val: numVal("2")})), "[2]{id}:\n  1\n  2"},
		{
			"list of non-uniform objects",
			arrVal(
				objVal(Field{Key: "id", Val: numVal("1")}),
				objVal(Field{Key: "id", Val: numVal("2")}, Field{Key: "name", Val: strVal("Ada")}),
			),
			"[2]:\n  - id: 1\n  - id: 2\n    name: Ada",
		},
		{"arrays of arrays", arrVal(arrVal(numVal("1"), numVal("2")), arrVal()), "[2]:\n  - [2]: 1,2\n  - [0]:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeOK(t, tc.v); got != tc.want {
				t.Errorf("Encode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEncodeArrayItemWithUnknownKindErrors(t *testing.T) {
	cases := []struct {
		name string
		v    Value
	}{
		{"inline item", objVal(Field{Key: "xs", Val: arrVal(Value{Kind: Kind(99)})})},
		{"tabular cell", objVal(Field{Key: "xs", Val: arrVal(
			objVal(Field{Key: "a", Val: numVal("1")}, Field{Key: "b", Val: Value{Kind: Kind(99)}}),
		)})},
		{"list item", objVal(Field{Key: "xs", Val: arrVal(numVal("1"), Value{Kind: Kind(99)}, objVal())})},
		{"root array item", arrVal(Value{}, objVal())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Encode(tc.v); err == nil {
				t.Error("Encode expected error for unknown kind, got nil")
			}
		})
	}
}
