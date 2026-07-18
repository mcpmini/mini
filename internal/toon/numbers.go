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

func canonicalFloat(lexeme string) (string, error) {
	f, err := strconv.ParseFloat(lexeme, 64)
	if err != nil {
		// Overflow (too large for float64) is valid JSON per RFC 8259; pass the
		// lexeme through textually rather than erroring per spec §2 out-of-domain.
		if math.IsInf(f, 0) {
			return normalizeOverflowLexeme(lexeme), nil
		}
		return "", fmt.Errorf("toon: malformed number %q: %w", lexeme, err)
	}
	if f == 0 {
		return "0", nil
	}
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	}
	return canonicalExponent(f), nil
}

// normalizeOverflowLexeme converts an out-of-range lexeme to lowercase e with
// an explicit exponent sign, preserving the mantissa digits exactly.
func normalizeOverflowLexeme(lexeme string) string {
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

// FromJSON/FromAny already hand encodeNum a canonical lexeme; Values built by
// hand (e.g. "-0", "1.0") must still satisfy spec §2 on output.
func encodeNum(v Value) (string, error) {
	return canonicalizeNumber(v.Num)
}
