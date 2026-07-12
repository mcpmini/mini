package toon

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var canonicalNumberRE = regexp.MustCompile(`^-?(0|[1-9]\d*)(\.\d+)?(e-?\d+)?$`)

// canonicalizeNumber applies spec §2: integer lexemes (no '.', no e/E) keep
// their digits verbatim so values beyond 2^53 survive exactly; everything
// else goes through float64 formatting.
func canonicalizeNumber(lexeme string) (string, error) {
	if !strings.ContainsAny(lexeme, ".eE") {
		return canonicalInteger(lexeme), nil
	}
	return canonicalFloat(lexeme)
}

func canonicalInteger(lexeme string) string {
	neg := strings.HasPrefix(lexeme, "-")
	digits := strings.TrimPrefix(lexeme, "-")
	digits = strings.TrimPrefix(digits, "+")
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0"
	}
	if neg {
		return "-" + digits
	}
	return digits
}

func canonicalFloat(lexeme string) (string, error) {
	f, err := strconv.ParseFloat(lexeme, 64)
	if err != nil {
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

func canonicalExponent(f float64) string {
	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	sign := ""
	exp = strings.TrimPrefix(exp, "+")
	if strings.HasPrefix(exp, "-") {
		sign, exp = "-", exp[1:]
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	return mantissa + "e" + sign + exp
}

// encodeNum returns v.Num verbatim; it was already canonicalized when set
// (FromJSON/FromAny), so Encode never reformats it.
func encodeNum(v Value) (string, error) {
	if !canonicalNumberRE.MatchString(v.Num) {
		return "", fmt.Errorf("toon: malformed number %q", v.Num)
	}
	return v.Num, nil
}
