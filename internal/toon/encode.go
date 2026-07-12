package toon

import (
	"fmt"
	"strings"
)

const indentUnit = "  "

// Encode renders v as a TOON document per spec §5's root-form rules: a root
// object emits its fields at indent 0, a root array emits under a bare [N]
// header, a root scalar emits as a bare value.
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
	for _, f := range fields {
		if err := writeField(sb, f, depth); err != nil {
			return err
		}
	}
	return nil
}

func writeField(sb *strings.Builder, f Field, depth int) error {
	sb.WriteString(strings.Repeat(indentUnit, depth))
	return writeFieldBody(sb, f, depth)
}

// writeFieldBody renders a field whose line prefix (indent or list-item
// hyphen) has already been written; depth is the logical depth of the line.
func writeFieldBody(sb *strings.Builder, f Field, depth int) error {
	if f.Val.Kind == KindArray {
		ctx := arrayCtx{Key: encodeKey(f.Key), ItemDepth: depth + 1, AllowTabular: true, FieldEmpty: true}
		return writeArray(sb, f.Val.Items, ctx)
	}
	sb.WriteString(encodeKey(f.Key))
	sb.WriteString(":")
	return writeFieldValue(sb, f.Val, depth)
}

func writeFieldValue(sb *strings.Builder, v Value, depth int) error {
	if v.Kind == KindObject {
		sb.WriteString("\n")
		return writeFields(sb, v.Fields, depth+1)
	}
	s, err := encodePrimitive(v)
	if err != nil {
		return err
	}
	sb.WriteString(" ")
	sb.WriteString(s)
	sb.WriteString("\n")
	return nil
}

func encodePrimitive(v Value) (string, error) {
	switch v.Kind {
	case KindNull:
		return "null", nil
	case KindBool:
		return encodeBool(v.Bool), nil
	case KindNumber:
		return encodeNum(v)
	case KindString:
		return encodeString(v.Str), nil
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
