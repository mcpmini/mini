package toon

import (
	"fmt"
	"strings"
)

const indentUnit = "  "

// maxEncodeDepth bounds nesting because every line costs 2×depth indent bytes,
// so unbounded depth lets a small hostile upstream response amplify into a
// multi-GB encode. 64 is far beyond any real API payload.
const maxEncodeDepth = 64

// maxEncodeBytes bounds total output because deep non-uniform arrays amplify
// indent bytes per line, so an unbounded builder lets a small hostile
// upstream response balloon toward the 64MB upstream cap's worst case.
const maxEncodeBytes = 4 * 1024 * 1024

var errEncodeTooLarge = fmt.Errorf("toon: encoded output exceeds %d bytes", maxEncodeBytes)

func checkDepth(depth int) error {
	if depth > maxEncodeDepth {
		return fmt.Errorf("toon: nesting depth exceeds %d", maxEncodeDepth)
	}
	return nil
}

// Encode renders v as a TOON document per spec §5 root-form rules.
// See https://github.com/toon-format/spec/blob/main/SPEC.md#5-concrete-syntax-and-root-form
func Encode(v Value) (string, error) {
	switch v.Kind {
	case KindNull, KindBool, KindNumber, KindString:
		return encodePrimitive(v)
	case KindObject:
		return encodeRootObject(v.Fields)
	case KindArray:
		return encodeRootArray(v.Items)
	default:
		return "", fmt.Errorf("toon: unknown kind %d", v.Kind)
	}
}

func encodeRootArray(items []Value) (string, error) {
	if len(items) == 0 {
		return "[]", nil
	}
	var sb strings.Builder
	if err := writeArray(&sb, items, arrayCtx{ItemDepth: 1, AllowTabular: true}); err != nil {
		return "", err
	}
	return strings.TrimSuffix(sb.String(), "\n"), nil
}

func encodeRootObject(fields []Field) (string, error) {
	var sb strings.Builder
	if err := writeFields(&sb, fields, 0); err != nil {
		return "", err
	}
	return strings.TrimSuffix(sb.String(), "\n"), nil
}

func writeFields(sb *strings.Builder, fields []Field, depth int) error {
	if err := checkDepth(depth); err != nil {
		return err
	}
	if sb.Len() > maxEncodeBytes {
		return errEncodeTooLarge
	}
	for _, f := range fields {
		if err := writeField(sb, f, depth); err != nil {
			return err
		}
	}
	return nil
}

func writeField(sb *strings.Builder, f Field, depth int) error {
	if err := appendString(sb, strings.Repeat(indentUnit, depth)); err != nil {
		return err
	}
	return writeFieldBody(sb, f, depth)
}

// The caller has already written the line prefix (indent or list-item hyphen).
func writeFieldBody(sb *strings.Builder, f Field, depth int) error {
	if err := validateUTF8(f.Key); err != nil {
		return err
	}
	if f.Val.Kind == KindArray {
		ctx := arrayCtx{Key: encodeKey(f.Key), ItemDepth: depth + 1, AllowTabular: true, FieldEmpty: true}
		return writeArray(sb, f.Val.Items, ctx)
	}
	if err := appendString(sb, encodeKey(f.Key)); err != nil {
		return err
	}
	if err := appendString(sb, ":"); err != nil {
		return err
	}
	return writeFieldValue(sb, f.Val, depth)
}

func writeFieldValue(sb *strings.Builder, v Value, depth int) error {
	if v.Kind == KindObject {
		if err := appendString(sb, "\n"); err != nil {
			return err
		}
		return writeFields(sb, v.Fields, depth+1)
	}
	s, err := encodePrimitive(v)
	if err != nil {
		return err
	}
	if err := appendString(sb, " "); err != nil {
		return err
	}
	if err := appendString(sb, s); err != nil {
		return err
	}
	if err := appendString(sb, "\n"); err != nil {
		return err
	}
	return nil
}

func appendString(sb *strings.Builder, s string) error {
	if len(s) > maxEncodeBytes-sb.Len() {
		return errEncodeTooLarge
	}
	sb.WriteString(s)
	return nil
}

func encodePrimitive(v Value) (string, error) {
	switch v.Kind {
	case KindNull:
		return "null", nil
	case KindBool:
		return encodeBool(v.Bool), nil
	case KindNumber:
		s, err := encodeNum(v)
		if err != nil {
			return "", err
		}
		if len(s) > maxEncodeBytes {
			return "", errEncodeTooLarge
		}
		return s, nil
	case KindString:
		if err := validateUTF8(v.Str); err != nil {
			return "", err
		}
		s := encodeString(v.Str)
		if len(s) > maxEncodeBytes {
			return "", errEncodeTooLarge
		}
		return s, nil
	default:
		return "", fmt.Errorf("toon: unknown kind %d", v.Kind)
	}
}

func encodeBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
