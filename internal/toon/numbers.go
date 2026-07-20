package toon

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// canonicalizeNumber applies spec §2: integer lexemes (no '.', no e/E) keep
// their digits verbatim so values beyond 2^53 survive exactly; everything
// else goes through float64 formatting.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#2-data-model
func canonicalizeNumber(lexeme string) (string, error) {
	if !strings.ContainsAny(lexeme, ".eE") {
		return canonicalInteger(lexeme)
	}
	return canonicalFloat(lexeme)
}

func canonicalInteger(lexeme string) (string, error) {
	neg := strings.HasPrefix(lexeme, "-")
	digits := strings.TrimPrefix(lexeme, "-")
	digits = strings.TrimPrefix(digits, "+")
	if digits == "" || !isDigits(digits) {
		return "", fmt.Errorf("toon: malformed number %q", lexeme)
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0", nil
	}
	if neg {
		return "-" + digits, nil
	}
	return digits, nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// canonicalFloat parses decimal/exponent lexemes. Values outside float64's
// finite range — overflow (too large) and underflow (too small) — pass through
// textually per spec §2 out-of-domain policy.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#2-data-model
func canonicalFloat(lexeme string) (string, error) {
	f, err := strconv.ParseFloat(lexeme, 64)
	if err != nil {
		if math.IsInf(f, 0) {
			return passThroughOutOfRange(lexeme)
		}
		return "", fmt.Errorf("toon: malformed number %q: %w", lexeme, err)
	}
	if f == 0 {
		if hasMantissaNonZeroDigit(lexeme) {
			return passThroughOutOfRange(lexeme)
		}
		return "0", nil
	}
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	}
	return canonicalExponent(f), nil
}

// passThroughOutOfRange validates s as JSON-grammar-legal and returns it
// normalized (lowercase e, explicit exponent sign). Grammar validation is
// required even when ParseFloat succeeded, because ParseFloat accepts Go forms
// like "1.e-324" (no digits after decimal) that JSON forbids.
func passThroughOutOfRange(lexeme string) (string, error) {
	if !isValidJSONNumberLexeme(lexeme) {
		return "", fmt.Errorf("toon: malformed number %q", lexeme)
	}
	return normalizeOutOfRangeLexeme(lexeme), nil
}

func hasMantissaNonZeroDigit(lexeme string) bool {
	for _, c := range lexeme {
		if c == 'e' || c == 'E' {
			break
		}
		if c >= '1' && c <= '9' {
			return true
		}
	}
	return false
}

func normalizeOutOfRangeLexeme(lexeme string) string {
	lo := strings.ToLower(lexeme)
	mantissa, expStr, hasExp := strings.Cut(lo, "e")
	if !hasExp {
		return lo
	}
	if !strings.HasPrefix(expStr, "+") && !strings.HasPrefix(expStr, "-") {
		expStr = "+" + expStr
	}
	return mantissa + "e" + expStr
}

func canonicalExponent(f float64) string {
	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	sign := "+"
	if strings.HasPrefix(exp, "-") {
		sign, exp = "-", exp[1:]
	} else {
		exp = strings.TrimPrefix(exp, "+")
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	return mantissa + "e" + sign + exp
}

// isValidJSONNumberLexeme rejects Go-only number syntax like "1.e309" that
// strconv.ParseFloat accepts but RFC 8259 forbids (no digit after ".").
func isValidJSONNumberLexeme(s string) bool {
	i := consumeJSONInteger(s, 0)
	if i < 0 {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i = consumeDigitRun(s, i+1)
		if i < 0 {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		i = consumeDigitRun(s, i)
		if i < 0 {
			return false
		}
	}
	return i == len(s)
}

func consumeJSONInteger(s string, pos int) int {
	i := pos
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return -1
	}
	if s[i] == '0' {
		return i + 1
	}
	if s[i] < '1' || s[i] > '9' {
		return -1
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

func consumeDigitRun(s string, pos int) int {
	if pos >= len(s) || s[pos] < '0' || s[pos] > '9' {
		return -1
	}
	i := pos
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

// FromJSON/FromAny already hand encodeNum a canonical lexeme; Values built by
// hand (e.g. "-0", "1.0") must still satisfy spec §2 on output.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#2-data-model
func encodeNum(v Value) (string, error) {
	return canonicalizeNumber(v.Num)
}
