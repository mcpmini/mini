package toon

import "testing"

func TestNeedsQuoting(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty string", "", true},
		{"leading whitespace", " abc", true},
		{"trailing whitespace", "abc ", true},
		{"leading and trailing whitespace", " abc ", true},
		{"equals true", "true", true},
		{"equals false", "false", true},
		{"equals null", "null", true},
		{"numeric-like integer", "42", true},
		{"numeric-like negative decimal", "-3.14", true},
		{"numeric-like leading zero", "05", true},
		{"numeric-like exponent", "1e-6", true},
		{"numeric-like exponent uppercase", "1E+6", true},
		{"contains colon", "a:b", true},
		{"contains double quote", `a"b`, true},
		{"contains backslash", `a\b`, true},
		{"contains open bracket", "a[b", true},
		{"contains close bracket", "a]b", true},
		{"contains open brace", "a{b", true},
		{"contains close brace", "a}b", true},
		{"contains control char", "a\x01b", true},
		{"contains delimiter comma", "a,b", true},
		{"equals hyphen", "-", true},
		{"starts with hyphen", "-abc", true},
		{"plain word", "abc", false},
		{"internal space is safe", "hello world", false},
		{"unicode and emoji safe", "Hello 世界 👋", false},
		{"hyphen not at position 0 is safe", "a-b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsQuoting(tc.in); got != tc.want {
				t.Errorf("needsQuoting(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestEncodeStringEscapesRoundTripVisually(t *testing.T) {
	in := "a\"b\\c\nd\te"
	want := `"a\"b\\c\nd\te"`
	if got := encodeString(in); got != want {
		t.Errorf("encodeString(%q) = %q, want %q", in, got, want)
	}
}

func TestEncodeStringControlCharEscape(t *testing.T) {
	in := "a\x01b"
	want := "\"a\\u0001b\""
	if got := encodeString(in); got != want {
		t.Errorf("encodeString(%q) = %q, want %q", in, got, want)
	}
}

func TestEncodeStringUnquotedWhenSafe(t *testing.T) {
	if got := encodeString("hello"); got != "hello" {
		t.Errorf("encodeString(hello) = %q, want unquoted", got)
	}
}

func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"simple identifier", "name", "name"},
		{"underscore prefix", "_id", "_id"},
		{"digits and dots allowed", "a.b_2", "a.b_2"},
		{"leading digit requires quoting", "2fast", `"2fast"`},
		{"hyphen requires quoting", "my-key", `"my-key"`},
		{"space requires quoting", "my key", `"my key"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeKey(tc.key); got != tc.want {
				t.Errorf("encodeKey(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}
