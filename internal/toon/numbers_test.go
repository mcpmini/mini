package toon

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestCanonicalizeNumberBoundaryTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lower bound of decimal range", "1e-6", "0.000001"},
		{"just below decimal range uses exponent", "1e-7", "1e-7"},
		{"just below upper bound stays decimal", "1e20", "100000000000000000000"},
		{"upper bound uses exponent", "1e21", "1e+21"},
		{"trailing fractional zero drops to integer", "10.0", "10"},
		{"negative zero integer normalizes to zero", "-0", "0"},
		{"negative zero float normalizes to zero", "-0.0", "0"},
		{"uppercase E with plus sign canonicalizes", "1E+6", "1000000"},
		{"arbitrary precision integer survives verbatim", "123456789012345678901234567890", "123456789012345678901234567890"},
		{"exponent with fractional mantissa", "2.5e-7", "2.5e-7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalizeNumber(tc.in)
			if err != nil {
				t.Fatalf("canonicalizeNumber(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("canonicalizeNumber(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalizeNumberInvalidLexemeErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"double decimal point", "1.2.3"},
		// strconv.ParseFloat accepts Go-syntax "1.e309" (no digit after '.')
		// but JSON grammar requires at least one digit after the decimal point.
		{"overflow with decimal but no fractional digits", "1.e309"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := canonicalizeNumber(tc.in); err == nil {
				t.Errorf("canonicalizeNumber(%q) expected error, got nil", tc.in)
			}
		})
	}
}

func TestEncodeNumMalformed(t *testing.T) {
	cases := []struct {
		name string
		num  string
	}{
		{"empty", ""},
		{"not a number", "abc"},
		{"double decimal point", "1.2.3"},
		{"repeated decimal point", "1..2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Value{Kind: KindNumber, Num: tc.num}
			if _, err := Encode(v); err == nil {
				t.Errorf("Encode(Num=%q) expected error, got nil", tc.num)
			}
		})
	}
}

func TestEncodeNumCanonicalizesNonCanonicalLexemes(t *testing.T) {
	cases := []struct {
		name string
		num  string
		want string
	}{
		{"negative zero integer", "-0", "0"},
		{"trailing fractional zero", "1.0", "1"},
		{"positive exponent", "1e6", "1000000"},
		{"leading zero", "007", "7"},
		{"trailing fractional zeros", "1.5000", "1.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Value{Kind: KindNumber, Num: tc.num}
			got, err := Encode(v)
			if err != nil {
				t.Fatalf("Encode(Num=%q) unexpected error: %v", tc.num, err)
			}
			if got != tc.want {
				t.Errorf("Encode(Num=%q) = %q, want %q", tc.num, got, tc.want)
			}
		})
	}
}

func TestEncodeNumValid(t *testing.T) {
	v := Value{Kind: KindNumber, Num: "42"}
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	if got != "42" {
		t.Errorf("Encode(Num=42) = %q, want %q", got, "42")
	}
}

func TestCanonicalizeNumberOverflowLexemesPassThrough(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"positive overflow no sign", "1e309", "1e+309"},
		{"negative overflow uppercase E", "-1E309", "-1e+309"},
		{"fractional mantissa with sign", "1.5e+400", "1.5e+400"},
		{"huge decimal without exponent", strings.Repeat("9", 320) + ".5", strings.Repeat("9", 320) + ".5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalizeNumber(tc.in)
			if err != nil {
				t.Fatalf("canonicalizeNumber(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("canonicalizeNumber(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFromJSONOverflowNumberSucceeds(t *testing.T) {
	raw := json.RawMessage(`{"x":1e309}`)
	v, err := FromJSON(raw)
	if err != nil {
		t.Fatalf("FromJSON unexpected error: %v", err)
	}
	got, err := Encode(v)
	if err != nil {
		t.Fatalf("Encode unexpected error: %v", err)
	}
	want := "x: 1e+309"
	if got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestCanonicalizeNumberUnderflow(t *testing.T) {
	t.Run("underflow passes through textually not zero", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want string
		}{
			{"positive underflow", "1e-324", "1e-324"},
			{"negative underflow", "-1e-324", "-1e-324"},
			{"uppercase E normalizes to lowercase", "1E-324", "1e-324"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := canonicalizeNumber(tc.in)
				if err != nil {
					t.Fatalf("canonicalizeNumber(%q) unexpected error: %v", tc.in, err)
				}
				if got != tc.want {
					t.Errorf("canonicalizeNumber(%q) = %q, want %q", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("smallest surviving subnormal uses normal path", func(t *testing.T) {
		f, _ := strconv.ParseFloat("5e-324", 64)
		want := canonicalExponent(f)
		got, err := canonicalizeNumber("5e-324")
		if err != nil {
			t.Fatalf("canonicalizeNumber(%q) unexpected error: %v", "5e-324", err)
		}
		if got != want {
			t.Errorf("canonicalizeNumber(%q) = %q, want %q", "5e-324", got, want)
		}
	})

	t.Run("genuine zero spellings canonicalize to zero", func(t *testing.T) {
		zeros := []string{"0", "-0", "0.0", "0e5", "0.00e-10"}
		for _, z := range zeros {
			got, err := canonicalizeNumber(z)
			if err != nil {
				t.Fatalf("canonicalizeNumber(%q) unexpected error: %v", z, err)
			}
			if got != "0" {
				t.Errorf("canonicalizeNumber(%q) = %q, want %q", z, got, "0")
			}
		}
	})
}
