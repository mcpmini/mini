package toon

import (
	"fmt"
	"strings"
)

const indentUnit = "  "

// maxEncodeDepth bounds recursive validation and encoding to protect the host stack.
const maxEncodeDepth = 1024

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

func validateDepth(v Value, depth int) error {
	switch payload := v.payload.(type) {
	case objectValue:
		if err := checkDepth(depth); err != nil {
			return err
		}
		return validateFieldsDepth(payload, depth+1)
	case arrayValue:
		if err := checkDepth(depth); err != nil {
			return err
		}
		return validateItemsDepth(payload, depth+1)
	case nullValue, boolValue, numberValue, stringValue:
		return nil
	default:
		return nil
	}
}

func validateFieldsDepth(fields []Field, depth int) error {
	for _, f := range fields {
		if err := validateDepth(f.Val, depth); err != nil {
			return err
		}
	}
	return nil
}

func validateItemsDepth(items []Value, depth int) error {
	for _, item := range items {
		if err := validateDepth(item, depth); err != nil {
			return err
		}
	}
	return nil
}

// Encode renders v as a TOON document per spec §5 root-form rules.
// https://github.com/toon-format/spec/blob/62f16b369408180f1faf1cba7da1b46d1f336f12/SPEC.md?plain=1#L262
func Encode(v Value) (string, error) {
	if err := validateDepth(v, 0); err != nil {
		return "", err
	}
	switch payload := v.payload.(type) {
	case nullValue, boolValue, numberValue, stringValue:
		return encodePrimitive(v)
	case objectValue:
		return encodeRootObject(payload)
	case arrayValue:
		return encodeRootArray(payload)
	default:
		return "", fmt.Errorf("toon: unknown kind %d", v.kind())
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
	v := Value{payload: objectValue(fields)}
	if cols, ok := keyedTabularCols(v); ok {
		var sb strings.Builder
		if err := writeKeyedTabular(&sb, "", v, cols, 1); err != nil {
			return "", err
		}
		return strings.TrimSuffix(sb.String(), "\n"), nil
	}
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
	if f.Val.kind() == KindArray {
		ctx := arrayCtx{Key: encodeKey(f.Key), ItemDepth: depth + 1, AllowTabular: true, FieldEmpty: true}
		return writeArray(sb, f.Val.items(), ctx)
	}
	if f.Val.kind() == KindObject {
		if cols, ok := keyedTabularCols(f.Val); ok {
			return writeKeyedTabular(sb, encodeKey(f.Key), f.Val, cols, depth+1)
		}
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
	if v.kind() == KindObject {
		if err := appendString(sb, "\n"); err != nil {
			return err
		}
		return writeFields(sb, v.fields(), depth+1)
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
	switch payload := v.payload.(type) {
	case nullValue:
		return "null", nil
	case boolValue:
		return encodeBool(bool(payload)), nil
	case numberValue:
		return encodeNumber(payload)
	case stringValue:
		return encodeText(payload)
	case objectValue, arrayValue:
		return "", fmt.Errorf("toon: kind %d is not primitive", v.kind())
	}
	return "", fmt.Errorf("toon: unknown kind %d", v.kind())
}

func encodeNumber(n numberValue) (string, error) {
	s, err := encodeNum(n)
	if err != nil {
		return "", err
	}
	if len(s) > maxEncodeBytes {
		return "", errEncodeTooLarge
	}
	return s, nil
}

func encodeText(text stringValue) (string, error) {
	if err := validateUTF8(string(text)); err != nil {
		return "", err
	}
	s := encodeString(string(text))
	if len(s) > maxEncodeBytes {
		return "", errEncodeTooLarge
	}
	return s, nil
}

func encodeBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
