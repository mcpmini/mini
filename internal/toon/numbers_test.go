package toon

import "testing"

func TestCanonicalizeNumberBoundaryTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lower bound of decimal range", "1e-6", "0.000001"},
		{"just below decimal range uses exponent", "1e-7", "1e-7"},
		{"just below upper bound stays decimal", "1e20", "100000000000000000000"},
		{"upper bound uses exponent", "1e21", "1e21"},
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
	if _, err := canonicalizeNumber("1.2.3"); err == nil {
		t.Error("canonicalizeNumber(\"1.2.3\") expected error, got nil")
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
		{"leading zero", "05"},
		{"trailing decimal point", "1."},
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
